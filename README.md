Forked from https://github.com/apoorvam/aws-s3-multipart-upload

## Example
`env`
```dotenv
awsAccessKeyID=keyID
awsSecretAccessKey=accessKey
awsBucketRegion=Region
awsBucketName=name
```
`server`
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