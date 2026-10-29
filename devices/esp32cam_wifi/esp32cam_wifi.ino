/*
 * ==============================================================================
 * Project : Algorithmics Face Recognition (methics)
 * Board   : AI Thinker ESP32-CAM   (Tools > Board > "AI Thinker ESP32-CAM")
 * Peran   : Kamera untuk ESP32 utama (devices/esp32_scanner) lewat Wi-Fi.
 *           MODE UJI: pengganti kabel UART (devices/esp32cam_uart) saat kamera
 *           belum bisa dicatu/dikabel bersama ESP32 utama.
 *
 * Kamera tetap terpasang di ESP32-CAM-MB dan dicatu dari USB MB. ESP32 utama
 * memanggil kamera di jaringan lokal; foto tetap hanya dikirim ke server Go
 * oleh ESP32 utama (dengan token + HMAC). Kamera tidak menghubungi server Go.
 *
 * Alur sama seperti versi UART:
 *   - Saat idle sensor kamera mati (power-down lewat pin PWDN).
 *   - Ultrasonik di ESP32 utama mendeteksi orang -> POST /on -> kamera menyala
 *     dan terus menangkap frame (stream) ke buffer PSRAM.
 *   - ESP32 utama memanggil GET /capture beberapa kali selama ±2 detik,
 *     lalu POST /off. Kamera juga mati sendiri bila idle IDLE_OFF_MS.
 *
 * Endpoint HTTP (port 80). Semua wajib header  X-Cam-Token: <CAM_TOKEN>.
 *   GET  /ping     -> 200 "PONG psram=<0|1> cam=<on|off>"
 *   POST /on       -> 200 "ON"           | 500 "ERR camera_init"
 *   GET  /capture  -> 200 image/jpeg     | 500 "ERR ..." (kamera menyala dulu bila mati)
 *   POST /off      -> 200 "OFF"
 *   token salah    -> 401
 * Nama jaringan (mDNS): http://methics-cam.local  (CAM_HOSTNAME)
 *
 * secrets.h (salin dari secrets.example.h): WIFI_SSID, WIFI_PASSWORD, CAM_TOKEN.
 * CAM_TOKEN harus SAMA dengan CAM_TOKEN di secrets.h ESP32 utama.
 *
 * Catatan daya: ESP32-CAM + Wi-Fi butuh arus puncak tinggi. Gunakan port USB
 * yang kuat (charger HP 5V 2A lebih stabil daripada port laptop). Bila log
 * menampilkan "Brownout detector was triggered", ganti sumber daya/kabel USB.
 * ==============================================================================
 */

#include "esp_camera.h"
#include <WiFi.h>
#include <WebServer.h>
#include <ESPmDNS.h>

#if __has_include("secrets.h")
#include "secrets.h"
#else
#error "Salin secrets.example.h menjadi secrets.h lalu isi WIFI_SSID, WIFI_PASSWORD, CAM_TOKEN."
#endif

// ---------------------------------------------------------------------------
// Konfigurasi
// ---------------------------------------------------------------------------
#define CAM_HOSTNAME     "methics-cam"   // http://methics-cam.local ; SAMAKAN dengan CAM_MDNS_NAME di ESP32 utama

// Sama dengan versi UART agar hasil pengenalan sebanding.
#define FRAME_SIZE       FRAMESIZE_QVGA  // 320x240
#define JPEG_QUALITY     10              // 4..63, kecil = lebih bagus & lebih besar
#define JPEG_QUALITY_FALLBACK 18         // dipakai bila hasil melebihi MAX_JPEG_BYTES
#define MAX_JPEG_BYTES   24000           // SAMAKAN dengan MAX_IMAGE_BYTES di ESP32 utama

#define WARMUP_FRAMES    4               // frame awal dibuang agar exposure/white balance stabil
#define IDLE_OFF_MS      15000UL         // kamera mati sendiri bila tidak ada permintaan

#define USE_FLASH        0               // 1 = LED flash (GPIO4) menyala selama kamera ON
#define FLASH_LED_PIN    4

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

WebServer server(80);
bool cameraOn = false;
bool hasPsram = false;
unsigned long lastRequest = 0;

void flash(bool on) {
#if USE_FLASH
  digitalWrite(FLASH_LED_PIN, on ? HIGH : LOW);
#else
  (void)on;
#endif
}

// Matikan sensor: lepas driver lalu tahan PWDN tinggi (OV2640 power-down).
void cameraOff() {
  if (cameraOn) {
    esp_camera_deinit();
    cameraOn = false;
    Serial.println("[CAM] OFF");
  }
  pinMode(PWDN_GPIO_NUM, OUTPUT);
  digitalWrite(PWDN_GPIO_NUM, HIGH);
  flash(false);
}

// Nyalakan sensor dan mulai menangkap terus-menerus (stream ke buffer).
bool cameraOnNow() {
  if (cameraOn) return true;
  camera_config_t config = {};
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
  config.jpeg_quality = JPEG_QUALITY;
  config.frame_size = FRAME_SIZE;
  if (hasPsram) {
    config.fb_count = 2;                        // stream: driver terus mengisi 2 buffer
    config.fb_location = CAMERA_FB_IN_PSRAM;
    config.grab_mode = CAMERA_GRAB_LATEST;      // /capture selalu dapat frame terbaru
  } else {
    config.fb_count = 1;
    config.fb_location = CAMERA_FB_IN_DRAM;
    config.grab_mode = CAMERA_GRAB_WHEN_EMPTY;
  }

  unsigned long t0 = millis();
  esp_err_t err = esp_camera_init(&config);
  if (err != ESP_OK) {
    Serial.printf("[CAM] init gagal 0x%x\n", err);
    cameraOff();
    return false;
  }
  sensor_t* s = esp_camera_sensor_get();
  s->set_quality(s, JPEG_QUALITY);
  flash(true);
  for (int i = 0; i < WARMUP_FRAMES; i++) {
    camera_fb_t* fb = esp_camera_fb_get();
    if (fb) esp_camera_fb_return(fb);
  }
  cameraOn = true;
  Serial.printf("[CAM] ON dalam %lu ms (PSRAM=%d)\n", millis() - t0, hasPsram);
  return true;
}

camera_fb_t* grabFrame() {
  camera_fb_t* fb = esp_camera_fb_get();
  if (!hasPsram && fb) {
    esp_camera_fb_return(fb);   // tanpa PSRAM frame pertama bisa basi
    fb = esp_camera_fb_get();
  }
  return fb;
}

// Semua endpoint wajib X-Cam-Token yang sama dengan CAM_TOKEN.
bool authorized() {
  lastRequest = millis();
  if (server.header("X-Cam-Token") == CAM_TOKEN) return true;
  server.send(401, "text/plain", "ERR unauthorized");
  Serial.printf("[HTTP] token salah dari %s\n", server.client().remoteIP().toString().c_str());
  return false;
}

void handlePing() {
  if (!authorized()) return;
  server.send(200, "text/plain", String("PONG psram=") + (hasPsram ? 1 : 0) + " cam=" + (cameraOn ? "on" : "off"));
}

void handleOn() {
  if (!authorized()) return;
  if (cameraOnNow()) server.send(200, "text/plain", "ON");
  else server.send(500, "text/plain", "ERR camera_init");
}

void handleOff() {
  if (!authorized()) return;
  cameraOff();
  server.send(200, "text/plain", "OFF");
}

void handleCapture() {
  if (!authorized()) return;
  if (!cameraOnNow()) {
    server.send(500, "text/plain", "ERR camera_init");
    return;
  }
  camera_fb_t* fb = grabFrame();
  if (fb && fb->len > MAX_JPEG_BYTES) {
    sensor_t* s = esp_camera_sensor_get();
    esp_camera_fb_return(fb);
    s->set_quality(s, JPEG_QUALITY_FALLBACK);
    fb = grabFrame();
    s->set_quality(s, JPEG_QUALITY);
  }
  if (!fb) {
    server.send(500, "text/plain", "ERR capture");
    return;
  }
  if (fb->len > MAX_JPEG_BYTES) {
    server.send(500, "text/plain", String("ERR too_big ") + fb->len);
    esp_camera_fb_return(fb);
    return;
  }
  server.setContentLength(fb->len);
  server.send(200, "image/jpeg", "");
  server.client().write(fb->buf, fb->len);
  Serial.printf("[CAM] kirim %u byte %ux%u\n", (unsigned)fb->len, fb->width, fb->height);
  esp_camera_fb_return(fb);
}

void connectWiFi() {
  WiFi.mode(WIFI_STA);
  WiFi.setSleep(false);   // latensi rendah saat diminta frame
  WiFi.setHostname(CAM_HOSTNAME);
  WiFi.begin(WIFI_SSID, WIFI_PASSWORD);
  Serial.print("[WiFi] menghubungkan");
  unsigned long t0 = millis();
  while (WiFi.status() != WL_CONNECTED && millis() - t0 < 20000) {
    delay(400);
    Serial.print('.');
  }
  Serial.println();
  if (WiFi.status() == WL_CONNECTED) {
    Serial.printf("[WiFi] IP kamera: %s  <-- isi CAM_HOST di ESP32 utama bila mDNS tidak jalan\n",
                  WiFi.localIP().toString().c_str());
  } else {
    Serial.println("[WiFi] gagal; restart dalam 5 detik");
    delay(5000);
    ESP.restart();
  }
}

void setup() {
  Serial.begin(115200);
#if USE_FLASH
  pinMode(FLASH_LED_PIN, OUTPUT);
#endif
  hasPsram = psramFound();

  bool ok = cameraOnNow();   // uji sekali, lalu matikan sampai ada orang
  cameraOff();
  Serial.printf("[CAM] kamera %s (idle: mati)\n", ok ? "OK" : "GAGAL");

  connectWiFi();
  if (MDNS.begin(CAM_HOSTNAME)) {
    MDNS.addService("http", "tcp", 80);
    Serial.printf("[mDNS] http://%s.local\n", CAM_HOSTNAME);
  }

  const char* headers[] = {"X-Cam-Token"};
  server.collectHeaders(headers, 1);
  server.on("/ping", HTTP_GET, handlePing);
  server.on("/on", HTTP_POST, handleOn);
  server.on("/off", HTTP_POST, handleOff);
  server.on("/capture", HTTP_GET, handleCapture);
  server.onNotFound([]() { server.send(404, "text/plain", "ERR not_found"); });
  server.begin();
  Serial.println("[HTTP] siap di port 80");
}

void loop() {
  server.handleClient();
  if (cameraOn && millis() - lastRequest >= IDLE_OFF_MS) cameraOff();   // pengaman
  if (WiFi.status() != WL_CONNECTED) {
    static unsigned long lastTry = 0;
    if (millis() - lastTry > 5000) {
      lastTry = millis();
      WiFi.reconnect();
    }
  }
  delay(1);
}
