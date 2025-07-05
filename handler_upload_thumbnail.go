package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

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

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// TODO: implement the upload here
	//setting max memory to 10MB
	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)

	//get the image data from the form using r.Formfile to get the file data and headers
	fileData, fileHeader, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "file not found", err)
		return
	}
	defer fileData.Close()
	//using the mime.parsemediatype to get media type from header
	mediaType := fileHeader.Header.Get("Content-Type")
	if extensionType, _, err := mime.ParseMediaType(mediaType); extensionType != "image/png" && extensionType != "image/jpeg" {
		respondWithError(w, http.StatusBadRequest, "wrong media type to upload", err)
		return
	}

	//reading all image data into a byte slice
	//var imageData []byte
	imageData, err := io.ReadAll(fileData)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "couldn't read file", err)
		return
	}

	//get the video metadata from the sqlite database
	dbVideo, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "couldnt find video", err)
		return
	}

	if dbVideo.CreateVideoParams.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Wrong user id", err)
		return
	}

	//save thumbnail to global map
	thumbNail := thumbnail{data: imageData, mediaType: mediaType}
	videoThumbnails[videoID] = thumbNail

	//updating handler to store the files on the file system on the /assets directory
	//path is /assets/<videoID>.<file extension>
	file_extension := strings.Split(mediaType, "/")[1]

	var filepathURL = filepath.Join(cfg.assetsRoot, videoIDString+"."+file_extension)
	//use os.create to create the new file
	filePath, err := os.Create(filepathURL)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "couldnt create thumbnail file", err)
		return
	}
	defer filePath.Close()
	//copy the contents from the multipart.file to the new disk file using io.copy
	_, err = io.Copy(filePath, fileData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to copy to disk", err)
		return
	}

	//update the thumbnail url
	var thumbnailUrl = fmt.Sprintf("http://localhost:%s/%s/%s.%s", cfg.port, cfg.assetsRoot, videoID, mediaType)

	dbVideo.ThumbnailURL = &thumbnailUrl
	cfg.db.UpdateVideo(dbVideo)

	//respond with the update JSON of the video's metadata
	respondWithJSON(w, http.StatusOK, dbVideo)

}
