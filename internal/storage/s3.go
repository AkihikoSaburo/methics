// Package storage menyimpan foto wajah di object storage yang kompatibel
// dengan S3 (dipakai: Garage). Klien ditulis dengan pustaka standar saja
// (AWS Signature Version 4, gaya path) agar tidak menambah dependensi dan
// cukup untuk empat operasi yang dibutuhkan: Put, Get, Delete, HeadBucket.
package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// ErrNotFound dikembalikan Get bila objek tidak ada.
var ErrNotFound = errors.New("object not found")

// Config adalah koneksi ke satu bucket S3.
type Config struct {
	Endpoint        string // mis. http://10.100.1.11:3900
	Region          string // s3_region di garage.toml, mis. "garage"
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	Timeout         time.Duration
}

// Enabled bernilai true bila semua field wajib terisi.
func (c Config) Enabled() bool {
	return c.Endpoint != "" && c.Bucket != "" && c.AccessKeyID != "" && c.SecretAccessKey != ""
}

// S3 adalah klien minimal untuk satu bucket.
type S3 struct {
	cfg      Config
	endpoint *url.URL
	client   *http.Client
	now      func() time.Time
}

// NewS3 memvalidasi konfigurasi dan membuat klien.
func NewS3(cfg Config) (*S3, error) {
	if !cfg.Enabled() {
		return nil, errors.New("S3 endpoint, bucket, access key, dan secret wajib diisi")
	}
	endpoint, err := url.Parse(strings.TrimRight(cfg.Endpoint, "/"))
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return nil, fmt.Errorf("S3_ENDPOINT tidak valid: %q", cfg.Endpoint)
	}
	if cfg.Region == "" {
		cfg.Region = "garage"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	return &S3{cfg: cfg, endpoint: endpoint, client: &http.Client{Timeout: cfg.Timeout}, now: time.Now}, nil
}

// Bucket mengembalikan nama bucket.
func (s *S3) Bucket() string { return s.cfg.Bucket }

// Put mengunggah objek.
func (s *S3) Put(ctx context.Context, key, contentType string, data []byte) error {
	headers := map[string]string{}
	if contentType != "" {
		headers["content-type"] = contentType
	}
	response, err := s.do(ctx, http.MethodPut, key, headers, data)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return expectStatus(response, http.StatusOK)
}

// Get mengunduh objek. Pemanggil wajib menutup reader.
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, string, int64, error) {
	response, err := s.do(ctx, http.MethodGet, key, nil, nil)
	if err != nil {
		return nil, "", 0, err
	}
	if response.StatusCode == http.StatusNotFound {
		response.Body.Close()
		return nil, "", 0, ErrNotFound
	}
	if err := expectStatus(response, http.StatusOK); err != nil {
		response.Body.Close()
		return nil, "", 0, err
	}
	return response.Body, response.Header.Get("Content-Type"), response.ContentLength, nil
}

// Delete menghapus objek. Objek yang sudah tidak ada tidak dianggap error.
func (s *S3) Delete(ctx context.Context, key string) error {
	response, err := s.do(ctx, http.MethodDelete, key, nil, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil
	}
	return expectStatus(response, http.StatusNoContent, http.StatusOK)
}

// HeadBucket memeriksa bahwa bucket ada dan kunci akses berlaku.
func (s *S3) HeadBucket(ctx context.Context) error {
	response, err := s.do(ctx, http.MethodHead, "", nil, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return expectStatus(response, http.StatusOK)
}

func (s *S3) do(ctx context.Context, method, key string, headers map[string]string, body []byte) (*http.Response, error) {
	canonicalURI := "/" + uriEncode(s.cfg.Bucket, false)
	if key != "" {
		canonicalURI += "/" + uriEncode(key, true)
	}
	prefix := strings.TrimRight(s.endpoint.EscapedPath(), "/")
	canonicalURI = prefix + canonicalURI

	request, err := http.NewRequestWithContext(ctx, method, s.endpoint.Scheme+"://"+s.endpoint.Host+canonicalURI, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body == nil {
		request.Body = http.NoBody
		request.ContentLength = 0
	}
	payloadHash := sha256Hex(body)
	signed := map[string]string{
		"host":                 s.endpoint.Host,
		"x-amz-content-sha256": payloadHash,
	}
	for name, value := range headers {
		signed[strings.ToLower(name)] = value
	}
	authorization, amzDate := signV4(method, canonicalURI, "", signed, payloadHash,
		s.cfg.AccessKeyID, s.cfg.SecretAccessKey, s.cfg.Region, "s3", s.now().UTC())
	for name, value := range signed {
		if name != "host" {
			request.Header.Set(name, value)
		}
	}
	request.Header.Set("X-Amz-Date", amzDate)
	request.Header.Set("Authorization", authorization)
	return s.client.Do(request)
}

// signV4 menghitung header Authorization AWS Signature Version 4. headers
// berisi nama header huruf kecil (wajib menyertakan host); x-amz-date
// ditambahkan di sini.
func signV4(method, canonicalURI, canonicalQuery string, headers map[string]string, payloadHash, accessKey, secretKey, region, service string, t time.Time) (string, string) {
	amzDate := t.Format("20060102T150405Z")
	date := t.Format("20060102")
	all := make(map[string]string, len(headers)+1)
	for name, value := range headers {
		all[strings.ToLower(name)] = strings.Join(strings.Fields(value), " ")
	}
	all["x-amz-date"] = amzDate
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	sort.Strings(names)
	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name + ":" + all[name] + "\n")
	}
	signedHeaders := strings.Join(names, ";")
	canonicalRequest := strings.Join([]string{method, canonicalURI, canonicalQuery, canonicalHeaders.String(), signedHeaders, payloadHash}, "\n")
	scope := date + "/" + region + "/" + service + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonicalRequest))
	key := hmacSHA256([]byte("AWS4"+secretKey), date)
	key = hmacSHA256(key, region)
	key = hmacSHA256(key, service)
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, stringToSign))
	return fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s", accessKey, scope, signedHeaders, signature), amzDate
}

// uriEncode mengikuti aturan URI-encode SigV4: karakter unreserved tetap,
// sisanya %XX huruf besar; "/" dipertahankan bila keepSlash.
func uriEncode(value string, keepSlash bool) string {
	var out strings.Builder
	for _, b := range []byte(value) {
		switch {
		case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9', b == '-', b == '_', b == '.', b == '~':
			out.WriteByte(b)
		case b == '/' && keepSlash:
			out.WriteByte(b)
		default:
			fmt.Fprintf(&out, "%%%02X", b)
		}
	}
	return out.String()
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(data))
	return mac.Sum(nil)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func expectStatus(response *http.Response, accepted ...int) error {
	for _, status := range accepted {
		if response.StatusCode == status {
			return nil
		}
	}
	detail, _ := io.ReadAll(io.LimitReader(response.Body, 512))
	return fmt.Errorf("S3 %s %s: status %d: %s", response.Request.Method, response.Request.URL.Path, response.StatusCode, strings.TrimSpace(string(detail)))
}
