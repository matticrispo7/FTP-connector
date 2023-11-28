# FTP connector


[![License:
MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
<p align="center">
<img src="images/golang.svg" width="220">
</p>

## Overview
The FTP Connector is a utility written in `Golang` to instantiate *N* FTP clients that connect to multiple independent FTP servers to keep track of the version of a file. Whenever a client detects a new version of a file, a copy of it is uploaded to a bucket in Google Cloud Storage.
If either the connection to Google Cloud Storage or the upload of a file fails for at most _M_ times in a row, the upload is disabled and that file, with all the subsequent ones, will be downloaded locally and stored in a Docker volume.
It utilizes multiple asynchronous threads to manage the connection to the servers concurrently.


## Features
- Create *N* FTP clients able to connect to multiple FTP servers
- Each client keeps track of the versions of files with a given extension
- Automatically upload the newer version of the file to a Google Cloud Storage bucket
- Instantiate an FTP Server to serve the files locally (whether the connection to Cloud Storage fails)
- Utilizes multiple asynchronous threads to manage the connection to the servers concurrently

## Prerequisites
- [Docker](https://www.docker.com/get-started/)
- [Google Cloud credentials](https://cloud.google.com/docs/authentication/provide-credentials-adc?hl=it#how-to)

## Configuration
### FTP server
The FTP server works only in passive mode to avoid NAT related problems. If you want to allow the access to the server both with Ethernet and WLAN connection you have to configure two different _.env_ files with the following variables:
- **FTP_USER**: username to login
- **FTP_PASS**: password to login
- **PORT**: FTP connection port. Default value 21.
- **PASV_ENABLE**: flag to enable passive mode. Default value "**YES**"
- **PASV_MIN_PORT**: min port number to be used for passive connections. Default value **21100**
- **PASV_MAX_PORT**: max port number to be used for passive connections. Default value **21110**
- **PASV_ADDRESS**: the server's IP address (Ethernet/WLAN) returned to the client

### FTP client
Each FTP client must have the following structure:
- **host**: the IP of the FTP server
- **user**: the username to login to the FTP server
- **password**: the password to login to the FTP server
- **server_name**: the hostname associated to the server's IP
- **dir_path**: the absolute path of the server's directory which contains the files to track 
- **file_ext**: the extension of the files to track (*csv*, *txt*, ...). If set to *, all files with any extension in *dir_path* are tracked
- **sampling**: sampling time [ms] to check for files updates
- **retry_conn**: the timeout [ms] to retry the connection to the server (if the previous one failed)

### Google Cloud Storage
The Google Cloud Storage configuration is done via the following variables:
- **credentials_path**: the path to the JSON credentials path (the one got from the above link)
- **project_id**: the Google Cloud project ID
- **bucket_name**: the Cloud Storage bucket's name (where files will be uploaded)
- **upload_path**: the relative path, in the bucket, where files will be uploaded
- **retry_conn**: the timeout [ms] to retry the connection to Cloud Storage
- **retry_upload**: the timeout [ms] to retry the upload of a file
- **file_upload_attempts**: the max number of upload attempts for a given file (if the max attempts are reached, that file and all the subsequent ones will be downloaded locally and the upload to Cloud Storage stopped) 
- **connection_attempts**: the max number of connection attempts to Cloud Storage (if the max attempts are reached, that file and all the subsequent ones will be downloaded locally and the upload to Cloud Storage 

## How to Use
1) Copy the provided *docker-compose.yml* in a given path.
2) You have to create a bunch of folders (sorry about that). You can just copy and paste the following commands:
```sh
mkdir ftp && cd ftp
mkdir files client server && mkdir client/auth
cd server && mkdir log && cd log && mkdir ethernet-connection wlan-connection
```
3) Copy both *credentials.json* and *conf.json* in *client/auth*. 
Then copy the two *.env* files in *ftp/server*


4) Load the docker images
```sh
docker load -i ftp-client.tar
docker load -i vsftpd.tar
```

5) Run the Docker images
```sh
docker-compose up -d
```

## License

This utility is open-source and released under the [MIT
License](https://github.com/matticrispo7/FTP-connector/blob/main/LICENSE)
