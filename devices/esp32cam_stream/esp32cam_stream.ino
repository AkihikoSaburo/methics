// ESP32-CAM (AI Thinker) - Stream MJPEG + kontrol resolusi dari web
// Board: "AI Thinker ESP32-CAM" | Serial Monitor: 115200
//
// Halaman web  : http://<IP>/            (stream, foto, atur resolusi, kualitas, mirror, flip, flash)
// Foto tunggal : http://<IP>/capture     (JPEG, bisa dipakai ESP32 lain)
// Stream MJPEG : http://<IP>:81/stream
// Kontrol      : http://<IP>/control?var=framesize&val=QVGA
//                var: framesize | quality | hmirror | vflip | flash

#include "esp_camera.h"
#include <WiFi.h>
#include "esp_http_server.h"

const char* WIFI_SSID = "Poco X3 Oguri";       // <-- isi dengan WiFi kamu
const char* WIFI_PASS = "qwertyuiop";   // <-- isi dengan password WiFi kamu

// Pin AI Thinker ESP32-CAM
#define PWDN_GPIO_NUM     32
#define RESET_GPIO_NUM    -1
#define XCLK_GPIO_NUM      0
#define SIOD_GPIO_NUM     26
#define SIOC_GPIO_NUM     27
#define Y9_GPIO_NUM       35
#define Y8_GPIO_NUM       34
#define Y7_GPIO_NUM       39
#define Y6_GPIO_NUM       36
#define Y5_GPIO_NUM       21
#define Y4_GPIO_NUM       19
#define Y3_GPIO_NUM       18
#define Y2_GPIO_NUM        5
#define VSYNC_GPIO_NUM    25
#define HREF_GPIO_NUM     23
#define PCLK_GPIO_NUM     22
#define FLASH_LED_PIN      4

#define PART_BOUNDARY "123456789000000000000987654321"
static const char* STREAM_CONTENT_TYPE = "multipart/x-mixed-replace;boundary=" PART_BOUNDARY;
static const char* STREAM_BOUNDARY     = "\r\n--" PART_BOUNDARY "\r\n";
static const char* STREAM_PART         = "Content-Type: image/jpeg\r\nContent-Length: %u\r\n\r\n";

httpd_handle_t web_httpd = NULL;
httpd_handle_t stream_httpd = NULL;

framesize_t maxFrameSize = FRAMESIZE_SVGA;   // diatur otomatis di initCamera()

// ---------------------------------------------------------------------------
// Halaman web
// ---------------------------------------------------------------------------
static const char INDEX_HTML[] PROGMEM = R"rawliteral(
<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>ESP32-CAM</title>
<style>
  body{font-family:sans-serif;background:#111;color:#eee;margin:0;padding:12px;text-align:center}
  img{max-width:100%;min-height:60px;background:#000;border-radius:6px}
  .row{margin:10px 0}
  button,select{padding:8px 14px;margin:3px;border-radius:6px;border:0;background:#1a8f6b;color:#fff;font-size:15px}
  select{background:#333}
  label{margin:0 8px}
  input[type=range]{width:180px;vertical-align:middle}
</style>
</head>
<body>
  <h3>ESP32-CAM</h3>
  <img id="view" src="">
  <div class="row">
    <button id="btnStream">Start Stream</button>
    <button id="btnShot">Foto</button>
  </div>
  <div class="row">
    Resolusi:
    <select id="size">
      <option value="QQVGA">QQVGA 160x120</option>
      <option value="QVGA" selected>QVGA 320x240</option>
      <option value="CIF">CIF 400x296</option>
      <option value="VGA">VGA 640x480</option>
      <option value="SVGA">SVGA 800x600</option>
      <option value="XGA">XGA 1024x768</option>
      <option value="SXGA">SXGA 1280x1024</option>
      <option value="UXGA">UXGA 1600x1200</option>
    </select>
  </div>
  <div class="row">
    Kualitas JPEG:
    <input type="range" id="quality" min="4" max="63" value="12">
    <span id="qv">12</span> (kecil = lebih bagus)
  </div>
  <div class="row">
    <label><input type="checkbox" id="mirror"> Mirror</label>
    <label><input type="checkbox" id="flip"> Flip</label>
    <label><input type="checkbox" id="flash"> Flash</label>
  </div>
<script>
  var host = location.hostname;
  var view = document.getElementById('view');
  var btnStream = document.getElementById('btnStream');
  var BLANK = 'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7';
  var streaming = false;

  function ctl(v, val) { return fetch('/control?var=' + v + '&val=' + val); }

  function startStream() {
    streaming = true;
    view.src = 'http://' + host + ':81/stream';
    btnStream.textContent = 'Stop Stream';
  }
  function stopStream() {
    streaming = false;
    view.src = BLANK;
    btnStream.textContent = 'Start Stream';
  }

  btnStream.onclick = function () { streaming ? stopStream() : startStream(); };
  document.getElementById('btnShot').onclick = function () {
    stopStream();
    view.src = '/capture?t=' + Date.now();
  };
  document.getElementById('size').onchange = function () { ctl('framesize', this.value); };

  var q = document.getElementById('quality');
  var qv = document.getElementById('qv');
  q.oninput = function () { qv.textContent = this.value; };
  q.onchange = function () { ctl('quality', this.value); };

  document.getElementById('mirror').onchange = function () { ctl('hmirror', this.checked ? 1 : 0); };
  document.getElementById('flip').onchange   = function () { ctl('vflip',   this.checked ? 1 : 0); };
  document.getElementById('flash').onchange  = function () { ctl('flash',   this.checked ? 1 : 0); };

  startStream();
</script>
</body>
</html>
)rawliteral";

// ---------------------------------------------------------------------------
// Kamera
// ---------------------------------------------------------------------------
bool initCamera() {
  camera_config_t config;
  config.ledc_channel = LEDC_CHANNEL_0;
  config.ledc_timer   = LEDC_TIMER_0;
  config.pin_d0 = Y2_GPIO_NUM;  config.pin_d1 = Y3_GPIO_NUM;
  config.pin_d2 = Y4_GPIO_NUM;  config.pin_d3 = Y5_GPIO_NUM;
  config.pin_d4 = Y6_GPIO_NUM;  config.pin_d5 = Y7_GPIO_NUM;
  config.pin_d6 = Y8_GPIO_NUM;  config.pin_d7 = Y9_GPIO_NUM;
  config.pin_xclk = XCLK_GPIO_NUM;
  config.pin_pclk = PCLK_GPIO_NUM;
  config.pin_vsync = VSYNC_GPIO_NUM;
  config.pin_href = HREF_GPIO_NUM;
  config.pin_sccb_sda = SIOD_GPIO_NUM;
  config.pin_sccb_scl = SIOC_GPIO_NUM;
  config.pin_pwdn = PWDN_GPIO_NUM;
  config.pin_reset = RESET_GPIO_NUM;
  config.xclk_freq_hz = 20000000;
  config.pixel_format = PIXFORMAT_JPEG;
  config.jpeg_quality = 12;

  if (psramFound()) {
    // Buffer dibuat untuk resolusi maksimum supaya resolusi bisa diubah bebas dari web
    config.frame_size = FRAMESIZE_UXGA;      // 1600x1200
    config.fb_count = 2;
    config.fb_location = CAMERA_FB_IN_PSRAM;
    config.grab_mode = CAMERA_GRAB_LATEST;   // selalu ambil frame terbaru (stream lebih mulus)
    maxFrameSize = FRAMESIZE_UXGA;
  } else {
    config.frame_size = FRAMESIZE_SVGA;      // 800x600
    config.fb_count = 1;
    config.fb_location = CAMERA_FB_IN_DRAM;
    config.grab_mode = CAMERA_GRAB_WHEN_EMPTY;
    maxFrameSize = FRAMESIZE_SVGA;
  }

  esp_err_t err = esp_camera_init(&config);
  if (err != ESP_OK) {
    Serial.printf("Kamera GAGAL init, error 0x%x\n", err);
    return false;
  }

  // Mulai dengan resolusi kecil supaya stream lancar
  sensor_t* s = esp_camera_sensor_get();
  s->set_framesize(s, FRAMESIZE_QVGA);
  s->set_quality(s, 12);

  Serial.println("Kamera OK");
  return true;
}

// ---------------------------------------------------------------------------
// Handler HTTP
// ---------------------------------------------------------------------------
static esp_err_t index_handler(httpd_req_t* req) {
  httpd_resp_set_type(req, "text/html");
  return httpd_resp_send(req, INDEX_HTML, HTTPD_RESP_USE_STRLEN);
}

static esp_err_t capture_handler(httpd_req_t* req) {
  camera_fb_t* fb = esp_camera_fb_get();
  if (!fb) {
    httpd_resp_send_500(req);
    return ESP_FAIL;
  }
  httpd_resp_set_type(req, "image/jpeg");
  httpd_resp_set_hdr(req, "Content-Disposition", "inline; filename=capture.jpg");
  httpd_resp_set_hdr(req, "Access-Control-Allow-Origin", "*");
  httpd_resp_set_hdr(req, "Cache-Control", "no-store");
  esp_err_t res = httpd_resp_send(req, (const char*)fb->buf, fb->len);
  esp_camera_fb_return(fb);
  return res;
}

static esp_err_t stream_handler(httpd_req_t* req) {
  esp_err_t res = httpd_resp_set_type(req, STREAM_CONTENT_TYPE);
  if (res != ESP_OK) return res;
  httpd_resp_set_hdr(req, "Access-Control-Allow-Origin", "*");

  char part[64];
  while (true) {
    camera_fb_t* fb = esp_camera_fb_get();
    if (!fb) {
      res = ESP_FAIL;
      break;
    }
    res = httpd_resp_send_chunk(req, STREAM_BOUNDARY, strlen(STREAM_BOUNDARY));
    if (res == ESP_OK) {
      size_t hlen = snprintf(part, sizeof(part), STREAM_PART, (unsigned)fb->len);
      res = httpd_resp_send_chunk(req, part, hlen);
    }
    if (res == ESP_OK) {
      res = httpd_resp_send_chunk(req, (const char*)fb->buf, fb->len);
    }
    esp_camera_fb_return(fb);
    if (res != ESP_OK) break;   // klien (browser) berhenti / terputus
  }
  return res;
}

static framesize_t parseFrameSize(const char* v) {
  if (!strcmp(v, "QQVGA")) return FRAMESIZE_QQVGA;
  if (!strcmp(v, "QVGA"))  return FRAMESIZE_QVGA;
  if (!strcmp(v, "CIF"))   return FRAMESIZE_CIF;
  if (!strcmp(v, "VGA"))   return FRAMESIZE_VGA;
  if (!strcmp(v, "SVGA"))  return FRAMESIZE_SVGA;
  if (!strcmp(v, "XGA"))   return FRAMESIZE_XGA;
  if (!strcmp(v, "SXGA"))  return FRAMESIZE_SXGA;
  if (!strcmp(v, "UXGA"))  return FRAMESIZE_UXGA;
  return FRAMESIZE_QVGA;
}

static esp_err_t control_handler(httpd_req_t* req) {
  char query[96];
  char var[24];
  char val[24];

  if (httpd_req_get_url_query_str(req, query, sizeof(query)) != ESP_OK ||
      httpd_query_key_value(query, "var", var, sizeof(var)) != ESP_OK ||
      httpd_query_key_value(query, "val", val, sizeof(val)) != ESP_OK) {
    httpd_resp_send_404(req);
    return ESP_FAIL;
  }

  sensor_t* s = esp_camera_sensor_get();
  int res = 0;

  if (!strcmp(var, "framesize")) {
    framesize_t fs = parseFrameSize(val);
    if (fs > maxFrameSize) fs = maxFrameSize;   // tanpa PSRAM maksimum SVGA
    res = s->set_framesize(s, fs);
  } else if (!strcmp(var, "quality")) {
    int q = atoi(val);
    if (q < 4) q = 4;
    if (q > 63) q = 63;
    res = s->set_quality(s, q);
  } else if (!strcmp(var, "hmirror")) {
    res = s->set_hmirror(s, atoi(val));
  } else if (!strcmp(var, "vflip")) {
    res = s->set_vflip(s, atoi(val));
  } else if (!strcmp(var, "flash")) {
    digitalWrite(FLASH_LED_PIN, atoi(val) ? HIGH : LOW);
  } else {
    res = -1;
  }

  if (res < 0) {
    httpd_resp_send_500(req);
    return ESP_FAIL;
  }
  httpd_resp_set_hdr(req, "Access-Control-Allow-Origin", "*");
  return httpd_resp_send(req, NULL, 0);
}

void startServers() {
  httpd_config_t config = HTTPD_DEFAULT_CONFIG();
  config.lru_purge_enable = true;

  httpd_uri_t index_uri   = { .uri = "/",        .method = HTTP_GET, .handler = index_handler,   .user_ctx = NULL };
  httpd_uri_t capture_uri = { .uri = "/capture", .method = HTTP_GET, .handler = capture_handler, .user_ctx = NULL };
  httpd_uri_t control_uri = { .uri = "/control", .method = HTTP_GET, .handler = control_handler, .user_ctx = NULL };
  httpd_uri_t stream_uri  = { .uri = "/stream",  .method = HTTP_GET, .handler = stream_handler,  .user_ctx = NULL };

  config.server_port = 80;
  if (httpd_start(&web_httpd, &config) == ESP_OK) {
    httpd_register_uri_handler(web_httpd, &index_uri);
    httpd_register_uri_handler(web_httpd, &capture_uri);
    httpd_register_uri_handler(web_httpd, &control_uri);
  }

  // Stream pakai server terpisah (port 81) agar tidak memblokir halaman dan kontrol
  config.server_port = 81;
  config.ctrl_port += 1;
  if (httpd_start(&stream_httpd, &config) == ESP_OK) {
    httpd_register_uri_handler(stream_httpd, &stream_uri);
  }
}

// ---------------------------------------------------------------------------
void setup() {
  Serial.begin(115200);
  pinMode(FLASH_LED_PIN, OUTPUT);
  digitalWrite(FLASH_LED_PIN, LOW);

  if (!initCamera()) return;

  WiFi.mode(WIFI_STA);
  WiFi.setSleep(false);   // tanpa mode hemat daya, latensi stream lebih rendah
  WiFi.begin(WIFI_SSID, WIFI_PASS);
  Serial.print("Konek WiFi");
  unsigned long t0 = millis();
  while (WiFi.status() != WL_CONNECTED) {
    delay(500);
    Serial.print(".");
    if (millis() - t0 > 30000) {
      Serial.println("\nGagal konek WiFi, restart...");
      ESP.restart();
    }
  }

  startServers();

  String ip = WiFi.localIP().toString();
  Serial.println("\nWiFi tersambung");
  Serial.println("Halaman web : http://" + ip + "/");
  Serial.println("Stream      : http://" + ip + ":81/stream");
  Serial.println("Foto        : http://" + ip + "/capture");
}

void loop() {
  // Sambungkan ulang jika WiFi terputus
  if (WiFi.status() != WL_CONNECTED) {
    Serial.println("WiFi terputus, mencoba sambung ulang...");
    WiFi.reconnect();
  }
  delay(10000);
}
