package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	//setting upload limit to 1GB
	uploadLimt := 1 << 20
	http.MaxBytesReader(w, r.Body, int64(uploadLimt))

	//extracting video Id from Url path parameter and parseing to uuid
	videoIDString := r.PathValue("videoID")
	parsedVideoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	//authenticating user to get user id
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	//getting video metadata
	video, err := cfg.db.GetVideo(parsedVideoID)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "couldnt get video", err)
		return
	}
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Not authorized to update this video", nil)
		return
	}
	//parsing the uploaded video file
	fileMultipart, fileHeader, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "couldn't parse video file", err)
		return
	}
	defer fileMultipart.Close()

	mediaType := fileHeader.Header.Get("Content-Type")
	if extensionType, _, err := mime.ParseMediaType(mediaType); extensionType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "type uploaded isn't mp4", err)
		return
	}

	//saving the uploaded file into a temporary file
	f, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		log.Fatal(err)
	}
	defer os.Remove(f.Name())

	_, err = io.Copy(f, fileMultipart)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "couldnt copy file", err)
		return
	}

	//resetting the temp file's pointer to the beginning
	f.Seek(0, io.SeekStart)

	//putting the object into s3 using putobject. We need bucket name,file key,file contents,content type
	file_extension := strings.Split(mediaType, "/")[1]
	key := make([]byte, 32)
	rand.Read(key)
	pathString := base64.RawURLEncoding.EncodeToString(key) + "." + file_extension

	_, err = cfg.s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &pathString,
		Body:        f,
		ACL:         types.ObjectCannedACLPublicRead,
		ContentType: &mediaType,
	})
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "couldnt upload to s3 bucket", err)
		return
	}

	//updatnig video url in database to reflect bucket. bucket address in the format https://<bucket-name>.s3.<region>.amazonaws.com/<key>

	var filepathURL = fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, pathString)
	video.VideoURL = &filepathURL
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "couldnt update video url", err)
		return
	}

}
