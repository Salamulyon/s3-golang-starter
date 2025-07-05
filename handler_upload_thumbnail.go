package main

import (
	"fmt"
	"io"
	"net/http"

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
	mediaType := fileHeader.Header.Get("Content-Type")

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

	//update the video metadata so it has a new thuumbnail url then update the database record
	var thumbnailUrl = fmt.Sprintf("http://localhost:%s/api/thumbnails/%s", cfg.port, videoID)
	dbVideo.ThumbnailURL = &thumbnailUrl
	cfg.db.UpdateVideo(dbVideo)

	//respond with the update JSON of the video's metadata
	respondWithJSON(w, http.StatusOK, dbVideo)

}
