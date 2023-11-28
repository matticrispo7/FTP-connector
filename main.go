package main

import (
	"fmt"
	"ftp-client/model"
	"ftp-client/utils"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

func main() {
	// load configuration file
	config := utils.LoadConfiguration("auth/conf.json")

	// first check if the necessary folders are present
	utils.CheckDirectory("log")
	utils.CheckDirectory("files")

	// create the "main" logger (the one that is used also for logging the info about the upload of files)
	mainLogger := utils.InitLogger(config, "main")

	// the storage is a map with key: IP and value:map(with key: filename and value: latest timestamp)
	storage := make(map[string]map[string]uint64)
	utils.LoadInfoDownloadedFile(storage)

	clientsFTP := config.Servers
	fmt.Println("Clients: ", clientsFTP)

	//  WaitGroup (for goroutines) and Mutex (to protect the access to shared resources)
	wg := &sync.WaitGroup{}
	mut := &sync.Mutex{}
	fileChannel := make(chan utils.FileToUpload, 20)
	uploadFiles := true // flag used to save files locally if there are errors uploading them to cloud storage

	// Create the Cloud Storage client
	clientCloudStorage, clientStorageErr := utils.NewClientCloudStorage(config.CloudStorage, mainLogger)
	if clientStorageErr != nil {
		uploadFiles = false
		fmt.Println("[Error] cloud storage client: ", clientStorageErr)
	}

	/*  Manage multiple FTP connections by creating as many goroutine as FTP connections needed.
	Each goroutine will have its own logger that will create the xxx.log file, where xxx is replaced
	as the host IP. */
	for _, clientConf := range clientsFTP {
		fmt.Println("Client => ", clientConf)
		wg.Add(1)
		logger := utils.InitLogger(config, clientConf.ServerName)
		// create the folder to which this client will store the files downloaded (final local path is: files/<host-IP>/)
		utils.CheckDirectory("files/" + clientConf.ServerName)
		// define the IIFE and start the new goroutine
		go func(wg *sync.WaitGroup, m *sync.Mutex, clientConf model.Server,
			logger *log.Logger, storage map[string]map[string]uint64, fileChannel chan utils.FileToUpload,
			upload *bool) {
			fmt.Println("[GOROUTINE] Client: ", clientConf)
			defer wg.Done()

			client, err := utils.NewClientFTP(clientConf, logger)
			if err != nil {
				logger.Println("Error creating client FTP")
			}

			// Get CWD (used later for listing files)
			cwd, err := client.CurrentDir()
			if err != nil {
				log.Fatalf("Error cwd: %s\n", err)
			}
			fmt.Printf("[GOROUTINE for %s] CWD: %s\n", clientConf.Host, cwd)

			var getFile bool // flag to check whether a file needs to be downloaded or not
			for {
				// list the files in the ftp server and select only ones with the right extension
				files, err := client.List(cwd)
				if err != nil {
					fmt.Println("Error listing file: ", err)
					goto NEXT // skip to the next iteration
				}
				for _, f := range files {
					getFile = false
					// If "f" is of type "file" then check if its extension matches the one in conf.json .
					// ==> If, in conf.json, the "file_ext" is set to *, track all the files with all the extensions
					if f.Type.String() == "file" {
						// get the file extension
						if data := strings.Split(f.Name, "."); data[len(data)-1] == clientConf.FileExtension || clientConf.FileExtension == "*" {
							// CHECK if the HOST is already present in the map. IF not, add it to it
							if _, ok := storage[clientConf.ServerName]; ok { // "ok" is a bool set to true if the key exists in the map
								fmt.Printf("[GOROUTINE for %s] Host %s ALREADY in the map. Checking if file exists %s\n", clientConf.Host, clientConf.Host, f.Name)
								// CHECK if the file is present in the map
								if _, ok := storage[clientConf.ServerName][f.Name]; ok {
									// THE FILE WAS ALREADY SAVED => check if the retrieved timestamp is > the one saved
									if uint64(f.Time.Unix()) > storage[clientConf.ServerName][f.Name] {
										m.Lock()
										storage[clientConf.ServerName][f.Name] = uint64(f.Time.Unix()) // update the file's info stored
										m.Unlock()
										fmt.Printf("[GOROUTINE for %s] ** NEWER VERSION found for file %s\n", clientConf.Host, f.Name)
										logger.Printf("Found update for file %s\n", f.Name)
										getFile = true
									} else {
										// the file has already the newest version
										fmt.Printf("[GOROUTINE for %s] The file %s has already the newest version\n", clientConf.Host, f.Name)
										getFile = false
									}
								} else {
									// th file, for the given host, was not previously saved => save the file in the map
									fmt.Printf("[GOROUTINE for %s] The file %s wasn't already saved\n", clientConf.Host, f.Name)
									logger.Printf("Found new file %s\n", f.Name)
									m.Lock()
									storage[clientConf.ServerName][f.Name] = uint64(f.Time.Unix())
									m.Unlock()
									getFile = true
								}
							} else {
								utils.UpdateMap(storage, m, clientConf.ServerName, f)
								logger.Printf("Found new file %s\n", f.Name)
								getFile = true
								fmt.Printf("%s not found. The file has been saved in the storage\n", f.Name)
							}

							// if the file needs to saved, upload it to the cloud. If there are problems, download it locally
							if getFile {
								fmt.Printf("[GOROUTINE for %s] ===> DOWNLOADING %s --- size: %d\n", clientConf.Host, f.Name, f.Size)
								reader, err := client.Retr(f.Name)
								if err != nil {
									logger.Printf("Error pulling file %s: %s\n", f.Name, err)
									fmt.Printf("[GOROUTINE for %s] Error pulling file %s: %s\n", clientConf.Host, f.Name, err)
									goto NEXT
								}

								// save files locally if there were errors uploading them to cloud
								if !*upload {
									fmt.Printf("++++++++++ Saving file %s locally\n", utils.GetFilenameFormatted(f, clientConf.FileExtension))
									// save the file locally at: /files/<host-IP>/
									outFile, err := os.Create("./files/" + clientConf.ServerName + "/" + utils.GetFilenameFormatted(f, clientConf.FileExtension))
									if err != nil {
										logger.Printf("Error creating local file %s: %s\n", f.Name, err)
										fmt.Printf("[GOROUTINE for %s] Error creating local file %s: %s\n", clientConf.Host, f.Name, err)
										goto NEXT
									}
									_, err = io.Copy(outFile, reader)
									if err != nil {
										logger.Printf("Error copying data to local file %s: %s\n", f.Name, err)
										fmt.Printf("[GOROUTINE for %s] Error copying data to local file %s: %s\n", clientConf.Host, f.Name, err)
										goto NEXT
									}

									logger.Printf("File %s successfully downloaded\n", f.Name)
									fmt.Printf("[GOROUTINE for %s] File %s successfully downloaded\n", clientConf.Host, f.Name)
									outFile.Close()
								} else { // send files to channel to upload them to cloud storage
									// read bytes, build the FileToUpload obj and send it to the channel
									data, err := io.ReadAll(reader)
									if err != nil {
										fmt.Println("Error reading file: ", err)
										goto NEXT
									}

									// get the original file's extension if "*" is specified in the config file for this FTP server
									tmp := strings.Split(f.Name, ".")
									fileExtension := tmp[len(tmp)-1]
									fileChannel <- utils.FileToUpload{
										Data:         data,
										Filename:     utils.GetFilenameFormatted(f, fileExtension),
										OriginalName: f.Name,
										Host:         clientConf.Host,
										ServerName:   clientConf.ServerName,
									}
									fmt.Printf("---- %s ADDED TO CHANNEL\n", utils.GetFilenameFormatted(f, fileExtension))
								}
								reader.Close()

							}
						}
					}
				}

			NEXT:
				// save filestorage to file
				utils.SaveFilesInfo(m, storage, "log")

				fmt.Println("------------------------------------------------------------------------")
				time.Sleep(time.Duration(clientConf.Sampling) * time.Millisecond)
			}
			fmt.Printf("[EXIT GOROUTINE FOR HOST %s]\n", clientConf.Host)
		}(wg, mut, clientConf, logger, storage, fileChannel, &uploadFiles)
	}

	/*
		If there was no error during the Cloud Storage client initialization, start the gorotuine
		responsible to extract data from the channel, build out the file and upload it to Cloud Storage
	*/
	if clientStorageErr == nil {
		wg.Add(1)
		go model.CloudStorageUpload(fileChannel, wg, clientCloudStorage, mut, &uploadFiles, config.CloudStorage, mainLogger)
	}

	wg.Wait()
}
