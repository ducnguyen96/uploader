## 1. Env
```dotenv
awsAccessKeyID=keyID
awsSecretAccessKey=accessKey
awsBucketRegion=Region
awsBucketName=name
```
## 2. Server
```go
package main

import (
	"log"

	"github.com/ducnguyen96/uploader"
	"github.com/gin-gonic/gin"
)

func main() {
	srv := gin.Default()
	srv.POST("/upload", uploader.Upload)

	// Run http server
	if err := srv.Run(":8080"); err != nil {
		log.Fatalf("could not run server: %v", err)
	}
}
```
## 3. Install gin
```shell
go get github.com/gin-gonic/gin
```
## 4. Get uploader
```shell
go get github.com/ducnguyen96/uploader@v0.0.13
```