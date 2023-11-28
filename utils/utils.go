package utils

import (
	"encoding/json"
	"fmt"
	"ftp-client/model"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jlaffaye/ftp"
	"gopkg.in/natefinch/lumberjack.v2"
)

/* Load configuration loads the configuration file and returns the corresponding object */
func LoadConfiguration(file string) model.Config {
	var config model.Config
	f, err := os.ReadFile(file)
	if err != nil {
		log.Println(err)
	}
	json.Unmarshal([]byte(f), &config)
	return config
}

// StringToInt converts the string parameter returning an int
func StringToInt(name string) int {
	val, err := strconv.Atoi(name)
	if err != nil {
		panic(fmt.Sprintf("Error converting env %s from string to int: %s\n", name, err))
	}
	return val
}

// GetIntEnv gets the env as string type, convert to int and return it
func GetIntEnv(name string) int {
	env := os.Getenv(name)
	val, err := strconv.Atoi(env)
	if err != nil {
		panic(fmt.Sprintf("Error converting env %s from string to int: %s\n", name, err))
	}
	return val
}

// InitLogger initialize a logger for each client that connects to a different host
func InitLogger(config model.Config, host string) *log.Logger {
	// load logger
	filename := fmt.Sprintf("./log/%s.log", host)
	e, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		fmt.Printf("error opening %s file: %v\n", filename, err)
		os.Exit(1)
	}
	logger := log.New(e, "", log.Ldate|log.Ltime|log.Lmsgprefix)
	logger.SetOutput(&lumberjack.Logger{
		Filename:   filename,
		MaxSize:    config.Log.Size,    // megabytes after which new file is created
		MaxBackups: config.Log.Backups, // number of backups
		MaxAge:     config.Log.Age,     // days
	})
	return logger
}

// CheckDirectory checks if directory exists in the CURRENT local path ("./").
// Otherwise it will create it.
func CheckDirectory(path string) {
	if stat, err := os.Stat(path); err == nil && stat.IsDir() {
		fmt.Printf("Directory %s found\n", path)
	} else {
		err = os.MkdirAll(path, 0750)
		if err != nil && !os.IsExist(err) {
			log.Fatal("Error creating directory ", path, ": ", err)
		}
		fmt.Printf("Directory created at %s\n", path)
	}
}

// ChangeDirectory changes the directory of an FTP client to the one specified by the env var
func ChangeDirectory(client *ftp.ServerConn, conf *model.Server, logger *log.Logger) error {
	var DIR_PATH []string // slice that contains the desidered path splitted
	dir, _ := client.CurrentDir()
	fmt.Printf("[GOROUTINE for %s] -> Current remote dir: %s\n", conf.Host, dir)
	if !strings.Contains(conf.DirPath, "/") && !strings.Contains(conf.DirPath, "\\") {
		DIR_PATH = append(DIR_PATH, conf.DirPath)
	} else if strings.Contains(conf.DirPath, "/") {
		DIR_PATH = strings.Split(conf.DirPath, "/")
	} else {
		DIR_PATH = strings.Split(conf.DirPath, "\\")
	}

	for _, value := range DIR_PATH {
		fmt.Printf("[GOROUTINE for %s] -> Changing dir to %s\n", conf.Host, value)
		// change CWD
		err := client.ChangeDir(value)
		if err != nil {
			logger.Printf("Error while changing directory to %s\n", value)
			return err
		}
	}
	return nil
}

/*
LoadInfoDownloadedFile will unmarshal the "/log/log.json" file to the map
provided as parameter
*/
func LoadInfoDownloadedFile(storage map[string]map[string]uint64) {
	// read the json file as byte array (if the file exists)
	b, err := os.ReadFile("./log/log.json")
	if err != nil {
		fmt.Println("Error reading log.json: ", err)
	}
	// if the file exists (#bytes read > 0) unmarshal the json into the map
	if len(b) > 0 {
		json.Unmarshal(b, &storage)
	}

}

/*
SaveFilesInfo will marshal the given map and write it to the file.
The mutex passed as parameter is used to lock the map while writing it (to avoid race conditions)
*/
func SaveFilesInfo(mut *sync.Mutex, storage map[string]map[string]uint64, dir string) {
	mut.Lock()
	jsonStr, err := json.Marshal(storage)
	if err != nil {
		mut.Unlock() // free the resource if there are errors
		fmt.Printf("Error while saving to file: %s\n", err)
		log.Fatal(err)
	}

	err = os.WriteFile("./log/log.json", jsonStr, 0644) // RW permission for user, R only for Group and Others
	if err != nil {
		mut.Unlock() // free the resource if there are errors
		log.Fatal(err)
	}
	mut.Unlock()
}

/*
GetFilenameFormatted will convert the filename provided to the following format: <filename>__<date>__<hours-minutes-seconds>.<extension>
*/
func GetFilenameFormatted(file *ftp.Entry, ext string) string {
	extension := ext // get the file extension associated to this specific FTP connection
	fname := strings.Split(file.Name, ".")[0]
	y, m, d := time.Unix(file.Time.Unix(), 0).UTC().Date()
	hh := time.Unix(file.Time.Unix(), 0).UTC().Hour()
	mm := time.Unix(file.Time.Unix(), 0).UTC().Minute()
	ss := time.Unix(file.Time.Unix(), 0).UTC().Second()
	filename := fmt.Sprintf("%s__%d-%d-%d_%d-%d-%d.%s", fname, d, int(m), y, hh, mm, ss, extension)
	return filename
}

/*
UpdateMap is used to store file's information (name and timestamp for each host) with the newer version found for the given file.
*/
func UpdateMap(storage map[string]map[string]uint64, m *sync.Mutex, serverName string, f *ftp.Entry) {
	m.Lock()
	if _, ok := storage[serverName]; !ok { // initialize the inner map (with initial key == HOST) and insert the value
		storage[serverName] = map[string]uint64{}
	}
	// the file wasn't previously downloaded => save its info in the storage
	storage[serverName][f.Name] = uint64(f.Time.Unix())
	m.Unlock()
}

/*
NewClientFTP will initialize an FTP client starting from the configuration object passed as parameter.
That client is then returned
*/
func NewClientFTP(conf model.Server, logger *log.Logger) (*ftp.ServerConn, error) {
	for {
		client, err := ftp.Dial(conf.Host+":21", ftp.DialWithTimeout(5*time.Second)) // connect to HOST at port 21
		if err != nil {
			fmt.Printf("[GOROUTINE for %s] Cannot reach server\n", conf.Host)
		} else {
			fmt.Printf("[GOROUTINE for %s] Connection opened\n", conf.Host)
			err = client.Login(conf.User, conf.Password)
			if err != nil {
				fmt.Printf("[GOROUTINE for %s] Error occurred during login\n", conf.Host)
			} else {
				fmt.Printf("[GOROUTINE for %s] Login succeded\n", conf.Host)
				err := ChangeDirectory(client, &conf, logger)
				if err != nil {
					fmt.Println("Error while changing dir: ", err)
					return nil, fmt.Errorf("error creating FTP client")
				}
				return client, nil
			}
		}
		time.Sleep(time.Duration(conf.RetryConnection) * time.Millisecond)
	}
}
