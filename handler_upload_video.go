package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
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

	//checking to see the aspect ratio of the video
	aspectRatio, err := getVideoAspectRatio(f.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "couldnt get aspect ratio", err)
		return
	}
	var aspectRatioString string
	switch aspectRatio {
	case "16:9":
		aspectRatioString = "landscape"
	case "9:16":
		aspectRatioString = "portrait"
	default:
		aspectRatioString = "other"
	}

	//key = path.Join(aspectRatioString, key)
	//resetting the temp file's pointer to the beginning
	f.Seek(0, io.SeekStart)

	//updating the video to add pre processing
	processedFilePath, err := processVideoForFastStart(f.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "couldnt pre process video", err)
		return
	}
	defer os.Remove(processedFilePath)

	processedVideo, err := os.Open(processedFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "couldnt open processed video path", err)
		return
	}
	defer processedVideo.Close()

	//putting the object into s3 using putobject. We need bucket name,file key,file contents,content type
	file_extension := strings.Split(mediaType, "/")[1]
	key := make([]byte, 32)
	rand.Read(key)
	pathString := aspectRatioString + "/" + base64.RawURLEncoding.EncodeToString(key) + "." + file_extension
	//pathString := cfg.s3Bucket + "," + aspectRatioString + "." + file_extension

	_, err = cfg.s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: &cfg.s3Bucket,
		Key:    &pathString,
		Body:   processedVideo,
		//ACL:         types.ObjectCannedACLPublicRead,
		ContentType: &mediaType,
	})
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "couldnt upload to s3 bucket", err)
		return
	}

	//updating video url in database to reflect bucket. bucket address in the format https://<bucket-name>.s3.<region>.amazonaws.com/<key>

	//var filepathURL = fmt.Sprintf("%s,%s", cfg.s3Bucket, key)
	var filepathURL = fmt.Sprintf("%s/%s", cfg.s3CfDistribution, pathString)
	video.VideoURL = &filepathURL
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "couldnt update video url", err)
		return
	}

	//video, err = cfg.dbVideoToSignedVideo(video)
	// if err != nil {
	// 	respondWithError(w, http.StatusInternalServerError, "Couldn't generate presigned URL", err)
	// 	return
	// }

}

func getVideoAspectRatio(filePath string) (string, error) {

	type AspectRatio struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}

	type Streams struct {
		Stream []AspectRatio `json:"streams"`
	}

	//use the exec command to run ffprobe on the filepath to get video information like width and height
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)

	var buffer bytes.Buffer
	cmd.Stdout = &buffer
	err := cmd.Run()
	if err != nil {
		return "Couldnt not run ffprobe command", err
	}

	//unmarshalling the stdout into a json struct
	result := Streams{}
	decoder := json.NewDecoder(&buffer)

	if err = decoder.Decode(&result); err != nil {
		return "could not decode into json", err
	}

	//getting aspect ratio
	var portraitRatio float64 = 9.0 / 16.0
	var landscapeRatio float64 = 16.0 / 9.0
	const difference float64 = 0.01
	ratio := float64(result.Stream[0].Width) / float64(result.Stream[0].Height)

	if math.Abs((ratio - portraitRatio)) <= difference {
		fmt.Println("9:16")
		return "9:16", nil
	} else if math.Abs((ratio - landscapeRatio)) <= difference {
		fmt.Println("16:9")
		return "16:9", nil
	} else {
		fmt.Println("other")
		return "other", nil
	}

}

func processVideoForFastStart(filePath string) (string, error) {
	outputFilePath := filePath + ".processing"
	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", outputFilePath)
	err := cmd.Run()
	if err != nil {
		return "could not run command", err
	}

	return outputFilePath, nil
}

// func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
// 	if video.VideoURL == nil {
// 		return video, nil
// 	}
// 	parts := strings.Split(*video.VideoURL, ",")
// 	if len(parts) < 2 {
// 		return video, nil
// 	}
// 	bucket := parts[0]
// 	key := parts[1]
// 	presigned, err := generatePresignedURL(cfg.s3Client, bucket, key, 5*time.Minute)
// 	if err != nil {
// 		return video, err
// 	}
// 	video.VideoURL = &presigned
// 	return video, nil
// }

// func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
// 	presignClient := s3.NewPresignClient(s3Client)
// 	presignedUrl, err := presignClient.PresignGetObject(context.TODO(), &s3.GetObjectInput{
// 		Bucket: aws.String(bucket),
// 		Key:    aws.String(key),
// 	}, s3.WithPresignExpires(expireTime))
// 	if err != nil {
// 		return "", fmt.Errorf("failed to generate presigned URL: %v", err)
// 	}
// 	return presignedUrl.URL, nil
//}
