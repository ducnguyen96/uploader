package uploader

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/gin-gonic/gin"
	_ "github.com/joho/godotenv/autoload"
)

const (
	maxPartSize = int64(5 * 1024 * 1024)
	maxRetries  = 3
)

var (
	awsAccessKeyID     = os.Getenv("awsAccessKeyID")
	awsSecretAccessKey = os.Getenv("awsSecretAccessKey")
	awsBucketRegion    = os.Getenv("awsBucketRegion")
	awsBucketName      = os.Getenv("awsBucketName")
)

type Form struct {
	Files []*multipart.FileHeader `form:"files" binding:"required"`
}

func readFile(file *multipart.FileHeader) ([]byte, error) {
	// Get raw file bytes - no reader method
	openedFile, _ := file.Open()

	binaryFile, err := ioutil.ReadAll(openedFile)

	if err != nil {
		return nil, err
	}

	defer func(openedFile multipart.File) {
		err := openedFile.Close()
		if err != nil {
			log.Fatalf("Failed closing file %v", file.Filename)
		}
	}(openedFile)
	return binaryFile, nil
}

func Upload(c *gin.Context) {
	// Using `ShouldBind`
	var form Form
	_ = c.ShouldBind(&form)

	// Validate inputs
	valid, message := validateUploadFiles(form.Files)

	if !valid {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": message,
			"path":    "validateUploadFiles(form.Files)",
		})
		return
	}

	// get credentials
	creds := credentials.NewStaticCredentials(awsAccessKeyID, awsSecretAccessKey, "")
	_, err := creds.Get()
	if err != nil {
		fmt.Printf("bad credentials: %s", err)
		// Response to client
		c.JSON(http.StatusInternalServerError, gin.H{
			"message": "bad credentials",
			"path":    "creds.Get()",
		})
		return
	}

	cfg := aws.NewConfig().WithRegion(awsBucketRegion).WithCredentials(creds)
	ss, err := session.NewSession(cfg)

	if err != nil {
		fmt.Printf("Failed creating new session: %s", err)
		// Response to client
		c.JSON(http.StatusInternalServerError, gin.H{
			"message": "Failed creating new session",
			"path":    "session.NewSession(cfg)",
		})
		return
	}

	svc := s3.New(ss, cfg)

	now := time.Now()
	nowRFC3339 := now.Format(time.RFC3339)

	successPaths := make([]string, len(form.Files))
	pathNumber := 0
	for _, formFile := range form.Files {
		binaryFile, err := readFile(formFile)

		if err != nil {
			// Response to client
			c.JSON(http.StatusInternalServerError, gin.H{
				"message": "Failed read file",
				"path":    " readFile(formFile)",
			})
			return
		}

		path := "/media/" + nowRFC3339 + "-" + formFile.Filename
		fileType := http.DetectContentType(binaryFile)

		input := &s3.CreateMultipartUploadInput{
			Bucket:      aws.String(awsBucketName),
			Key:         aws.String(path),
			ContentType: aws.String(fileType),
		}

		resp, err := svc.CreateMultipartUpload(input)
		if err != nil {
			fmt.Println(err.Error())
			// Response to client
			c.JSON(http.StatusInternalServerError, gin.H{
				"message": "Failed CreateMultipartUpload",
				"path":    "svc.CreateMultipartUpload(input)",
			})
			return
		}
		fmt.Println("Created multipart upload request")

		var curr, partLength int64
		var remaining = formFile.Size
		var completedParts []*s3.CompletedPart
		partNumber := 1
		for curr = 0; remaining != 0; curr += partLength {
			if remaining < maxPartSize {
				partLength = remaining
			} else {
				partLength = maxPartSize
			}
			// Upload binaries part
			completedPart, err := uploadPart(svc, resp, binaryFile[curr:curr+partLength], partNumber)

			// If upload this part fail
			// Make an abort upload error and exit
			if err != nil {
				fmt.Println(err.Error())
				err := abortMultipartUpload(svc, resp)
				if err != nil {
					fmt.Println(err.Error())
				}
				// Response to client
				c.JSON(http.StatusInternalServerError, gin.H{
					"message": "Failed abortMultipartUpload",
					"path":    "abortMultipartUpload(svc, resp)",
				})
				return
			}
			// else append completed part to a whole
			remaining -= partLength
			partNumber++
			completedParts = append(completedParts, completedPart)
		}

		completeResponse, err := completeMultipartUpload(svc, resp, completedParts)
		if err != nil {
			fmt.Println(err.Error())
			// Response to client
			c.JSON(http.StatusInternalServerError, gin.H{
				"message": "Failed completeMultipartUpload",
				"path":    "completeMultipartUpload(svc, resp, completedParts)",
			})
			return
		}

		fmt.Printf("Successfully uploaded file: %s\n", completeResponse.String())

		// Dereference the pointer
		successPaths[pathNumber] = *completeResponse.Location
		pathNumber++

		// Save to disk
		//`formFile` has io.reader method
		//err := c.SaveUploadedFile(formFile, formFile.Filename)
		//if err != nil {
		//	log.Fatalf("Failed saving file %v to disk", formFile.Filename)
		//}
	}

	c.JSON(http.StatusOK, gin.H{
		"paths": successPaths,
	})
}

func completeMultipartUpload(svc *s3.S3, resp *s3.CreateMultipartUploadOutput, completedParts []*s3.CompletedPart) (*s3.CompleteMultipartUploadOutput, error) {
	completeInput := &s3.CompleteMultipartUploadInput{
		Bucket:   resp.Bucket,
		Key:      resp.Key,
		UploadId: resp.UploadId,
		MultipartUpload: &s3.CompletedMultipartUpload{
			Parts: completedParts,
		},
	}
	return svc.CompleteMultipartUpload(completeInput)
}

func uploadPart(svc *s3.S3, resp *s3.CreateMultipartUploadOutput, fileBytes []byte, partNumber int) (*s3.CompletedPart, error) {
	tryNum := 1
	partInput := &s3.UploadPartInput{
		Body:          bytes.NewReader(fileBytes),
		Bucket:        resp.Bucket,
		Key:           resp.Key,
		PartNumber:    aws.Int64(int64(partNumber)),
		UploadId:      resp.UploadId,
		ContentLength: aws.Int64(int64(len(fileBytes))),
	}

	for tryNum <= maxRetries {
		uploadResult, err := svc.UploadPart(partInput)
		if err != nil {
			if tryNum == maxRetries {
				if aerr, ok := err.(awserr.Error); ok {
					return nil, aerr
				}
				return nil, err
			}
			fmt.Printf("Retrying to upload part #%v\n", partNumber)
			tryNum++
		} else {
			fmt.Printf("Uploaded part #%v\n", partNumber)
			return &s3.CompletedPart{
				ETag:       uploadResult.ETag,
				PartNumber: aws.Int64(int64(partNumber)),
			}, nil
		}
	}
	return nil, nil
}

func abortMultipartUpload(svc *s3.S3, resp *s3.CreateMultipartUploadOutput) error {
	fmt.Println("Aborting multipart upload for UploadId#" + *resp.UploadId)
	abortInput := &s3.AbortMultipartUploadInput{
		Bucket:   resp.Bucket,
		Key:      resp.Key,
		UploadId: resp.UploadId,
	}
	_, err := svc.AbortMultipartUpload(abortInput)
	return err
}

func validateUploadFiles(files []*multipart.FileHeader) (bool, string) {
	for _, formFile := range files {
		size := formFile.Size
		contentType := formFile.Header.Get("Content-Type")

		if size > maxPartSize {
			return false, "File too large"
		}

		if contentType != "image/jpeg" && contentType != "image/png" {
			return false, "Filetype is not supported"
		}
	}
	return true, "ok"
}

//func main() {
//	srv := gin.Default()
//	srv.POST("/upload", Upload)
//
//	if err := srv.Run(":" + "8080"); err != nil {
//		panic("Error")
//	}
//}
