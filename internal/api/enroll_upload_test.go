package api

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Server sungguhan memakai ReadTimeout/WriteTimeout 15 s. ReadEnrollImages
// harus memperpanjangnya agar 6 foto besar yang diproses lama tetap dijawab.
func TestReadEnrollImagesSixLargePhotosOutlastServerTimeouts(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		images, err := ReadEnrollImages(w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		time.Sleep(1500 * time.Millisecond) // lebih lama dari WriteTimeout server
		fmt.Fprintf(w, "%d", len(images))
	})
	server := httptest.NewUnstartedServer(handler)
	server.Config.ReadTimeout = time.Second
	server.Config.WriteTimeout = time.Second
	server.Start()
	t.Cleanup(server.Close)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	photo := bytes.Repeat([]byte{0xFF}, 4<<20) // 4 MB per foto, seperti foto HP
	for i := 1; i <= MaxEnrollPhotos; i++ {
		part, _ := writer.CreateFormFile("images", fmt.Sprintf("pose-%d.jpg", i))
		_, _ = part.Write(photo)
	}
	_ = writer.Close()
	response, err := http.Post(server.URL, writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatalf("request dropped (deadline not extended?): %v", err)
	}
	defer response.Body.Close()
	got, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || string(got) != "6" {
		t.Fatalf("want 200 with 6 photos, got %d %q", response.StatusCode, got)
	}
}

func TestReadEnrollImagesRejectsSevenPhotos(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for i := 0; i < MaxEnrollPhotos+1; i++ {
		part, _ := writer.CreateFormFile("images", "p.jpg")
		_, _ = part.Write([]byte("x"))
	}
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if _, err := ReadEnrollImages(httptest.NewRecorder(), request); err == nil {
		t.Fatal("7 photos must be rejected")
	}
}
