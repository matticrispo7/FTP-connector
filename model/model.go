package model

import (
	"context"
	"errors"
	"fmt"
	"ftp-client/utils"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

type Log struct {
	Size    int `json:"size"`
	Backups int `json:"backups"`
	Age     int `json:"age"`
}

type Server struct {
	Host            string `json:"host"`
	User            string `json:"user"`
	Password        string `json:"password"`
	ServerName      string `json:"server_name"`
	DirPath         string `json:"dir_path"`
	FileExtension   string `json:"file_ext"`
	Sampling        int    `json:"sampling"`
	RetryConnection int    `json:"retry_conn"`
}

type CloudStorage struct {
	CredentialsPath    string `json:"credentials_path"`
	ProjectID          string `json:"project_id"`
	BucketName         string `json:"bucket_name"`
	UploadPath         string `json:"upload_path"`
	RetryConnection    int    `json:"retry_conn"`
	RetryUpload        int    `json:"retry_upload"`
	FileUploadAttempts int    `json:"file_upload_attempts"`
	ConnectionAttempts int    `json:"connection_attempts"`
}

type Config struct {
	Servers      []Server     `json:"servers"`
	Log          Log          `json:"log"`
	CloudStorage CloudStorage `json:"cloud_storage"`
}

/* CLOUD STORAGE OBJECTS */
// client uploader to cloud storage
type ClientCloudStorage struct {
	Client     *storage.Client
	ProjectID  string
	BucketName string
	UploadPath string
}

// the structure of the file that will be uploaded
type FileToUpload struct {
	Data         []byte // bytes read from file downloaded from FTP server
	Filename     string // cloud storage object's name
	OriginalName string // filename as the original retrieved from FTP server
	Host         string // host IP (used to save the object at the Host folder created in the bucket)
	ServerName   string // the "resolved" name of the Host
}

/*
NewClientCloudStorage will initialize a new ClientCloudStorage object with the given parameters
The return values are the Cloud Storage client and the error.
At most N=cs.ConnectionAttempts attempts of client initialization will be performed.
If all the attempts fail, it returns (nil,error). Otherwise it returns (client, nil)
If error != nil is returned, the FTP files will be stored locally and not uploaded to the cloud.
*/
func NewClientCloudStorage(cs CloudStorage, logger *log.Logger) (*ClientCloudStorage, error) {
	i := 0
	for {
		client, err := storage.NewClient(context.Background(), option.WithCredentialsFile(cs.CredentialsPath))
		fmt.Printf("[CloudStorage] client created: %+v\n", client)

		if err != nil {
			fmt.Println("Error init client cloud storage: ", err)
			if i == cs.ConnectionAttempts {
				fmt.Println("Max retry connection attempts. Returning invalid cloud storage client")
				logger.Printf("[Cloud Storage Client] Max retry connection attempts. All attempts failed creating Cloud Storage client")
				err = errors.New("all attempts failed creating Cloud Storage client")
				return nil, err
			}
		} else {
			return &ClientCloudStorage{client, cs.ProjectID, cs.BucketName, cs.UploadPath}, nil
		}
		i++
		time.Sleep(time.Duration(cs.RetryConnection) * time.Millisecond)
	}

}

/*
UploadFile will upload the given file (passed as parameter) to the cloud storage
object with the same name as the file
*/
func (c *ClientCloudStorage) UploadFile(file FileToUpload) error {
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, time.Second*50)
	defer cancel()

	// Upload the file to a cloud storage object https://adityarama1210.medium.com/simple-golang-api-uploader-using-google-cloud-storage-3d5e45df74a5
	filePath := fmt.Sprintf("%s/%s/%s", c.UploadPath, file.ServerName, file.Filename) // example path in bucket: FTP/<host-IP>/<filename.ext>
	wc := c.Client.Bucket(c.BucketName).Object(filePath).NewWriter(ctx)
	if _, err := wc.Write(file.Data); err != nil {
		return fmt.Errorf("io.Copy: %v", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("Writer.Close: %v", err)
	}
	return nil
}

/*
CloudStorageUpload will forever read from the given channel and extract the file that needs to
uploaded to the Cloud Storage bucket. The first time an upload, for a given file, fails N times in a row
(where N is set as the env. var UPLOAD_ATTEMPTS), from now on all the files will be stored locally
and not uploaded to cloud.

Those files there were already in the channel (sent from the other FTP client) will not be uploaded
but indeed stored locally.
The upload pointer param is used to inform the other gorotuines whether to send file to the channel (to be later uploaded)
or to store it locally.

This function accepts a logger as a parameter that is used to log the info about the upload of a file
*/
func CloudStorageUpload(ch <-chan FileToUpload, wg *sync.WaitGroup, client *ClientCloudStorage, m *sync.Mutex, upload *bool, cs CloudStorage, logger *log.Logger) {
	defer wg.Done()
	fmt.Println("[GOROUTINE UPLOAD FILE STARTED]")
	for {
		// get the file
		for obj := range ch {
			attempts := 1 // file upload attempts
			for {
				fmt.Printf("**** Uploading file %s/%s\n", obj.ServerName, obj.Filename)
				//filename :=  // path is: host/file.ext
				err := client.UploadFile(obj)
				if err != nil {
					fmt.Printf("[%s] Error uploading file %s: %s\n", obj.ServerName, obj.Filename, err)
					logger.Printf("[%s] Error uploading file %s\n", obj.ServerName, obj.OriginalName)
					if attempts == cs.FileUploadAttempts {
						// close this goroutine and start saving files locally
						fmt.Println("[Goroutine] MAX UPlOAD ATTEMPTS reached. Closing goroutine")
						logger.Printf("[%s] Max upload attempts reached. Upload to Cloud Storage is disabled\n", obj.ServerName)
						// tells the other goroutines to save files locally
						m.Lock()
						*upload = false
						m.Unlock()

						/* retrieve all the files in the channel and save them locally */
						fmt.Println("--- TOTAL files in channels: ", len(ch))
						filesToSaveLocally := make(map[string][]string)
						// save the current file and get the others
						filesToSaveLocally[obj.ServerName] = append(filesToSaveLocally[obj.ServerName], obj.OriginalName)
						getAllFilesFromChannel(ch, filesToSaveLocally)
						fmt.Printf("____ AllFiles: %v\n", filesToSaveLocally)
						wg.Add(1)
						go saveFilesLocallyFromChannel(wg, filesToSaveLocally, logger)
						return
					}
					attempts++
					time.Sleep(time.Duration(cs.RetryUpload) * time.Millisecond)
				} else {
					logger.Printf("[%s] File %s (%s) uploaded successfully\n", obj.ServerName, obj.OriginalName, obj.Filename)
					fmt.Printf("File %s (%s) uploaded successfully\n", obj.OriginalName, obj.Filename)
					break
				}
			}
		}
	}
}

/*
Get all the files from the channel (that were inserted into it before the upload to Cloud Storage failed) and save them in the map.
That map will be used later on to retrieve the info of the file that needs to be downloaded.
*/
func getAllFilesFromChannel(ch <-chan FileToUpload, m map[string][]string) {
	for f := range ch {
		fmt.Println("___ retrieved file: ", f.Filename)
		m[f.ServerName] = append(m[f.ServerName], f.OriginalName)
		if len(ch) == 0 {
			return
		}
	}
}

/*
saveFilesLocallyFromChannel saves all the files found for each host (saved in the map parameter)
locally. Those files are the ones that were previously send in the channel and need to be
manually saved (since this function will execute when the upload to cloud storage is disabled by some errors)
*/
func saveFilesLocallyFromChannel(wg *sync.WaitGroup, m map[string][]string, logger *log.Logger) {
	defer wg.Done()
	//clientsConf := ConfigureClients()
	clientsConf := utils.LoadConfiguration("auth/conf.json").Servers
	// loop over map, find the right ClientConfig (in the slice) and get the username, pwd, .. for this client
	for serverName := range m {
		for _, clientConf := range clientsConf {
			if serverName == clientConf.ServerName {
				fmt.Printf("[%s] Saving files for server %s\n", serverName, clientConf.ServerName)
				// create new FTP client
				ftpClient, err := utils.NewClientFTP(clientConf, logger)
				if err != nil {
					fmt.Println("---- ERROR creating client")
				}

				// loop over the files (saved for this client) and download each one
				for _, filename := range m[serverName] {
					reader, err := ftpClient.Retr(filename)
					if err != nil {
						fmt.Printf("[NEW GOROUTINE for %s] Error pulling file %s: %s\n", clientConf.ServerName, filename, err)
						logger.Printf("[%s] Error pulling file %s: %s\n", clientConf.ServerName, filename, err)
					}
					outFile, err := os.Create("./files/" + clientConf.ServerName + "/" + filename)
					if err != nil {
						fmt.Printf("[NEW GOROUTINE for %s] Error creating local file %s: %s\n", clientConf.ServerName, filename, err)
						logger.Printf("[%s] Error creating local file %s\n", clientConf.ServerName, filename, err)
					}
					_, err = io.Copy(outFile, reader)
					if err != nil {
						fmt.Printf("[NEW GOROUTINE for %s] Error copying data to local file %s: %s\n", clientConf.ServerName, filename, err)
						logger.Printf("[%s] Error copying data to local file %s\n", clientConf.ServerName, filename, err)
					}

					fmt.Printf("[NEW GOROUTINE for %s] File %s successfully downloaded \n", clientConf.ServerName, filename)
					logger.Printf("[%s] File %s successfully downloaded\n", clientConf.ServerName, filename)
					outFile.Close()
					reader.Close()
				}
			}

		}
	}
}
