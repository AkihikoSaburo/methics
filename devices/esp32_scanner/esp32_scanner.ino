/*
 * ==============================================================================
 * Project : Algorithmics Face Recognition (methics)
 * Board   : ESP32 Dev Module (ESP32-WROOM-32 / DevKit V1)
 * Peran   : ESP32 utama perangkat absensi.
 *
 *   1. Sensor ultrasonik HC-SR04 mendeteksi orang (<= DETECT_CM).
 *   2. Layar TFT menyala, buzzer berbunyi, ESP32 menyalakan ESP32-CAM ("ON")
 *      lewat Wi-Fi atau UART (CAM_LINK_WIFI). Kamera terus menangkap frame.
 *   3. Selama ±SCAN_WINDOW_MS (2 detik) ESP32 mengambil hingga
 *      SCAN_MAX_FRAMES frame, lalu mematikan kamera ("OFF").
 *   4. Semua frame dikirim sekaligus ke backend Go  POST /api/v1/scans
 *      dengan Bearer token, stempel waktu NTP, dan tanda tangan HMAC-SHA256.
 *      Go mencocokkan tiap frame dengan wajah terdaftar di MongoDB.
 *   5. Hasil tampil di layar + pola buzzer:
 *        HADIR                  wajah cocok di >= 2 frame (tercatat di dashboard
 *                               pada kelas mahasiswa itu)
 *        Wajah tidak Terdaftar  ada wajah tetapi tidak cocok dengan siapa pun
 *        Wajah tidak terdeteksi tidak ada wajah di semua frame
 *        DITINJAU               cocok di 1 frame saja / bukan kelas perangkat
 *   6. Setiap PING_INTERVAL_MS mengirim heartbeat  POST /api/v1/devices/ping
 *      agar dashboard menampilkan perangkat "Terhubung".
 *
 * Wiring:
 *   HC-SR04 : VCC->5V(VIN), GND->GND, TRIG->GPIO26, ECHO->GPIO27 (lewat pembagi 1k+2k)
 *   Buzzer  : (+)/IO->GPIO22, (-)->GND
 *   TFT     : sesuai User_Setup.h TFT_eSPI (backlight TFT_BL, default GPIO21)
 *   Kamera  : pilih lewat CAM_LINK_WIFI.
 *     1 = Wi-Fi (MODE UJI): ESP32-CAM menjalankan devices/esp32cam_wifi,
 *         tetap di MB dengan USB sendiri; TIDAK ada kabel antar-board.
 *         Isi CAM_TOKEN di secrets.h dan (bila mDNS gagal) CAM_HOST.
 *     0 = kabel UART: ESP32-CAM menjalankan devices/esp32cam_uart,
 *         ESP32 GPIO16 (RX2) <- ESP32-CAM GPIO15 (TX)
 *         ESP32 GPIO17 (TX2) -> ESP32-CAM GPIO14 (RX)
 *         GND <-> GND (wajib)
 *   Pastikan GPIO16/17 tidak dipakai di User_Setup.h TFT_eSPI. Pada modul
 *   ESP32-WROVER, GPIO16/17 dipakai PSRAM: ganti CAM_RX_PIN/CAM_TX_PIN.
 *
 * Library: TFT_eSPI (Bodmer), dengan User_Setup.h yang sama seperti sketch
 * algorithmics_engine_ultrasonic_buzzer dan LOAD_GFXFF aktif (font FreeSans).
 *
 * Perintah Serial Monitor (115200, newline): scan | ping | cam | camon | camoff | status
 * ==============================================================================
 */

#include <WiFi.h>
#include <time.h>
#include <SPI.h>
#include <TFT_eSPI.h>
#include <HTTPClient.h>
#include <ESPmDNS.h>
#include "mbedtls/md.h"

#if __has_include("secrets.h")
#include "secrets.h"
#else
#error "Salin secrets.example.h menjadi secrets.h lalu isi WIFI_SSID, WIFI_PASSWORD, DEVICE_TOKEN."
#endif

// ---------------------------------------------------------------------------
// Konfigurasi server & identitas perangkat
// ---------------------------------------------------------------------------
#define SERVER_HOST      "172.16.0.102"   // IP komputer yang menjalankan backend Go
#define SERVER_PORT      8080
#define DEVICE_ID        "ESP32-GED-A1"    // harus ada di koleksi devices (seed)
// Kosong = kelas otomatis: kehadiran dicatat di kelas mahasiswa yang cocok.
// Isi kode kelas (HURUF BESAR, mis. "IK24-A") bila alat dipasang khusus untuk
// satu kelas; mahasiswa kelas lain lalu tercatat "Perlu ditinjau".
#define CLASS_ID         ""

// Alat ini belum punya deteksi liveness (anti-spoof) sungguhan, jadi kirim
// false. Server tetap mencatat "Hadir" bila wajah yang sama cocok di
// beberapa frame (SCAN_MIN_MATCH_FRAMES di server, default 2).
#define LIVENESS_PASSED  false

#define PING_INTERVAL_MS 15000UL           // dashboard offline bila > 2 menit tanpa ping
#define HTTP_TIMEOUT_MS  20000UL
#define NTP_SERVER_1     "pool.ntp.org"
#define NTP_SERVER_2     "time.google.com"

// ---------------------------------------------------------------------------
// Kamera
// ---------------------------------------------------------------------------
// Cara ESP32 utama terhubung ke ESP32-CAM:
//   0 = kabel UART  -> upload devices/esp32cam_uart ke kamera (mode produksi)
//   1 = Wi-Fi       -> upload devices/esp32cam_wifi ke kamera (MODE UJI, tanpa
//                      kabel antar-board; kamera tetap di MB dengan USB sendiri)
#define CAM_LINK_WIFI    1

// Mode Wi-Fi: IP kamera (lihat Serial Monitor kamera: "[WiFi] IP kamera: ...").
// Kosong = cari otomatis lewat mDNS "methics-cam.local".
#define CAM_HOST         ""
#define CAM_MDNS_NAME    "methics-cam"     // SAMAKAN dengan CAM_HOSTNAME di esp32cam_wifi

// Mode UART:
#define CAM_RX_PIN       16
#define CAM_TX_PIN       17
#define CAM_BAUD         460800            // SAMAKAN dengan LINK_BAUD di esp32cam_uart

#define MAX_IMAGE_BYTES  24000             // SAMAKAN dengan MAX_JPEG_BYTES di sketch kamera
#define CAM_TIMEOUT_MS   4000UL            // batas satu frame
#define CAM_ON_TIMEOUT_MS 5000UL           // batas kamera menyala (init + pemanasan)

#if CAM_LINK_WIFI && !defined(CAM_TOKEN)
#error "Mode Wi-Fi: tambahkan  #define CAM_TOKEN \"...\"  ke secrets.h (sama dengan CAM_TOKEN di esp32cam_wifi/secrets.h)."
#endif

// Jendela scan: kamera stream, ESP32 mengambil hingga SCAN_MAX_FRAMES frame
// yang tersebar dalam SCAN_WINDOW_MS. 4 frame QVGA ±60 KB RAM.
#define SCAN_WINDOW_MS   2000UL
#define SCAN_MAX_FRAMES  4                 // maks 6 (batas server)
#define HEAP_RESERVE     40000             // sisakan heap untuk Wi-Fi/TLS saat menyimpan frame

// ---------------------------------------------------------------------------
// Sensor, buzzer, layar (sama dengan sketch algorithmics_engine_ultrasonic_buzzer)
// ---------------------------------------------------------------------------
#define ROT 2

#define TRIG_PIN 26
#define ECHO_PIN 27
#define DETECT_CM          20
#define MEASURE_INTERVAL   100UL
#define CONFIRM_HITS       2
#define RESULT_SHOW_MS     6000UL          // lama hasil tampil sebelum layar mati
#define SCAN_COOLDOWN_MS   6000UL          // jeda minimum antar scan

#define BUZZER_PIN       22
#define BUZZER_PASSIVE    0
#define BUZZER_ON_LEVEL  HIGH
#define BUZZER_FREQ      2000
#define BUZZER_CH        0

#ifndef TFT_BL
#define TFT_BL 21
#endif
#ifndef TFT_BACKLIGHT_ON
#define TFT_BACKLIGHT_ON HIGH
#endif

#define COLOR_ORANGE 0xFD20

TFT_eSPI tft = TFT_eSPI();

// Tipe yang dipakai di parameter/nilai balik fungsi WAJIB didefinisikan di sini,
// sebelum fungsi pertama: Arduino IDE menyisipkan prototipe otomatis semua
// fungsi tepat sebelum fungsi pertama, jadi tipe yang didefinisikan lebih bawah
// menyebabkan error "'Frames' does not name a type".

// Hasil satu request HTTP.
struct HttpResult {
  int status = -1;        // -1 = gagal terhubung / timeout
  String body;
};

// Kumpulan frame satu scan (buffer malloc, dibebaskan freeFrames).
struct Frames {
  uint8_t* data[SCAN_MAX_FRAMES];
  size_t len[SCAN_MAX_FRAMES];
  int count = 0;
};

bool screenOn = false;
unsigned long screenOffAt = 0;
unsigned long lastMeasure = 0;
unsigned long lastPing = 0;
unsigned long lastScan = 0;
uint8_t hits = 0;
bool armed = true;          // objek harus menjauh dulu sebelum scan berikutnya
uint8_t awayCount = 0;
bool cameraOk = false;
bool timeOk = false;

// ---------------------------------------------------------------------------
// Buzzer (non-blocking)
// ---------------------------------------------------------------------------
uint8_t beepsLeft = 0;
bool beepOn = false;
unsigned long beepTimer = 0;
unsigned int beepOnMs = 120, beepOffMs = 100;

void buzzerInit() {
#if BUZZER_PASSIVE
  #if ESP_ARDUINO_VERSION_MAJOR >= 3
    ledcAttach(BUZZER_PIN, BUZZER_FREQ, 8);
  #else
    ledcSetup(BUZZER_CH, BUZZER_FREQ, 8);
    ledcAttachPin(BUZZER_PIN, BUZZER_CH);
  #endif
#else
  pinMode(BUZZER_PIN, OUTPUT);
#endif
}

void buzzerWrite(bool on) {
#if BUZZER_PASSIVE
  #if ESP_ARDUINO_VERSION_MAJOR >= 3
    ledcWriteTone(BUZZER_PIN, on ? BUZZER_FREQ : 0);
  #else
    ledcWriteTone(BUZZER_CH, on ? BUZZER_FREQ : 0);
  #endif
#else
  digitalWrite(BUZZER_PIN, on ? BUZZER_ON_LEVEL : !BUZZER_ON_LEVEL);
#endif
}

void startBeep(uint8_t count, unsigned int onMs, unsigned int offMs) {
  beepsLeft = count;
  beepOnMs = onMs;
  beepOffMs = offMs;
  beepOn = false;
  beepTimer = millis() - offMs;   // bip pertama langsung
}

void updateBeep() {
  if (beepsLeft == 0 && !beepOn) return;
  unsigned long now = millis();
  if (!beepOn) {
    if (beepsLeft > 0 && now - beepTimer >= beepOffMs) {
      buzzerWrite(true);
      beepOn = true;
      beepTimer = now;
    }
  } else if (now - beepTimer >= beepOnMs) {
    buzzerWrite(false);
    beepOn = false;
    beepTimer = now;
    if (beepsLeft > 0) beepsLeft--;
  }
}

// Bunyikan pola sampai selesai (dipakai di sela proses blocking).
void beepBlocking(uint8_t count, unsigned int onMs, unsigned int offMs) {
  startBeep(count, onMs, offMs);
  while (beepsLeft > 0 || beepOn) {
    updateBeep();
    delay(5);
  }
}

// ---------------------------------------------------------------------------
// Layar
// ---------------------------------------------------------------------------
void setBacklight(bool on) {
  digitalWrite(TFT_BL, on ? TFT_BACKLIGHT_ON : !TFT_BACKLIGHT_ON);
  screenOn = on;
}

void screenFor(unsigned long ms) {
  setBacklight(true);
  screenOffAt = millis() + ms;
}

// GFX free font hanya punya ASCII; ganti karakter lain dengan '?'.
String asciiOnly(const String& in) {
  String out;
  for (size_t i = 0; i < in.length(); i++) {
    uint8_t c = (uint8_t)in[i];
    if (c >= 0x20 && c < 0x7F) out += (char)c;
    else if ((c & 0xC0) != 0x80) out += '?';   // satu '?' per karakter UTF-8
  }
  return out;
}

// Tulis teks di tengah dengan font terbesar yang muat.
void drawFit(const String& text, int y, uint16_t color, bool large) {
  const GFXfont* fonts[] = {&FreeSansBold18pt7b, &FreeSansBold12pt7b, &FreeSansBold9pt7b, &FreeSans9pt7b};
  int start = large ? 0 : 2;
  String s = asciiOnly(text);
  tft.setTextColor(color, TFT_BLACK);
  tft.setTextDatum(MC_DATUM);
  for (int i = start; i < 4; i++) {
    tft.setFreeFont(fonts[i]);
    if (tft.textWidth(s) <= tft.width() - 12 || i == 3) {
      tft.drawString(s, tft.width() / 2, y);
      return;
    }
  }
}

// Bungkus teks panjang (alasan peninjauan) ke beberapa baris.
void drawWrapped(const String& text, int y, uint16_t color, int maxLines) {
  tft.setFreeFont(&FreeSans9pt7b);
  tft.setTextColor(color, TFT_BLACK);
  tft.setTextDatum(MC_DATUM);
  String rest = asciiOnly(text);
  int maxW = tft.width() - 16;
  for (int line = 0; line < maxLines && rest.length(); line++) {
    int cut = rest.length();
    while (cut > 0 && tft.textWidth(rest.substring(0, cut)) > maxW) {
      int sp = rest.lastIndexOf(' ', cut - 1);
      cut = sp > 0 ? sp : cut - 1;
    }
    String part = rest.substring(0, cut);
    rest = rest.substring(cut);
    rest.trim();
    if (line == maxLines - 1 && rest.length()) part += "...";
    tft.drawString(part, tft.width() / 2, y + line * 22);
  }
}

void drawHeader() {
  tft.fillScreen(TFT_BLACK);
  tft.setFreeFont(&FreeSansBold12pt7b);
  tft.setTextDatum(MC_DATUM);
  tft.setTextColor(TFT_CYAN, TFT_BLACK);
  tft.drawString("Algorithmics", tft.width() / 2, 26);
  tft.drawFastHLine(tft.width() / 2 - 70, 46, 140, TFT_CYAN);
}

void drawFooter() {
  tft.setFreeFont(&FreeSans9pt7b);
  tft.setTextDatum(BC_DATUM);
  tft.setTextColor(TFT_DARKGREY, TFT_BLACK);
  String kelas = strlen(CLASS_ID) ? String(CLASS_ID) : String("Kelas otomatis");
  String s = kelas + "  " + (WiFi.status() == WL_CONNECTED ? "WiFi" : "no WiFi") +
             (cameraOk ? "  CAM" : "  no CAM");
  tft.drawString(s, tft.width() / 2, tft.height() - 4);
}

void showMessage(const String& title, uint16_t color, const String& line1, const String& line2) {
  drawHeader();
  int mid = tft.height() / 2;
  drawFit(title, mid - 40, color, true);
  if (line1.length()) drawFit(line1, mid + 4, TFT_WHITE, false);
  if (line2.length()) drawWrapped(line2, mid + 36, TFT_LIGHTGREY, 4);
  drawFooter();
}

// ---------------------------------------------------------------------------
// Sensor
// ---------------------------------------------------------------------------
float readDistanceCm() {
  digitalWrite(TRIG_PIN, LOW);
  delayMicroseconds(2);
  digitalWrite(TRIG_PIN, HIGH);
  delayMicroseconds(10);
  digitalWrite(TRIG_PIN, LOW);
  unsigned long duration = pulseIn(ECHO_PIN, HIGH, 30000UL);
  if (duration == 0) return -1;
  return duration * 0.0343f / 2.0f;
}

// ---------------------------------------------------------------------------
// Wi-Fi & waktu
// ---------------------------------------------------------------------------
bool connectWiFi(unsigned long timeoutMs) {
  if (WiFi.status() == WL_CONNECTED) return true;
  WiFi.mode(WIFI_STA);
  WiFi.begin(WIFI_SSID, WIFI_PASSWORD);
  Serial.print("[WiFi] menghubungkan");
  unsigned long t0 = millis();
  while (WiFi.status() != WL_CONNECTED && millis() - t0 < timeoutMs) {
    delay(400);
    Serial.print('.');
  }
  Serial.println();
  if (WiFi.status() == WL_CONNECTED) {
    Serial.printf("[WiFi] IP %s\n", WiFi.localIP().toString().c_str());
    return true;
  }
  Serial.println("[WiFi] gagal");
  return false;
}

bool syncTime(unsigned long timeoutMs) {
  configTime(0, 0, NTP_SERVER_1, NTP_SERVER_2);   // UTC; server memakai RFC3339 UTC
  unsigned long t0 = millis();
  while (time(nullptr) < 1700000000 && millis() - t0 < timeoutMs) delay(200);
  timeOk = time(nullptr) >= 1700000000;
  Serial.printf("[NTP] %s\n", timeOk ? "tersinkron" : "GAGAL");
  return timeOk;
}

void rfc3339Now(char* out, size_t size) {
  time_t now = time(nullptr);
  struct tm t;
  gmtime_r(&now, &t);
  strftime(out, size, "%Y-%m-%dT%H:%M:%SZ", &t);
}

// ---------------------------------------------------------------------------
// Kriptografi (mbedTLS bawaan core ESP32)
// ---------------------------------------------------------------------------
void toHex(const uint8_t* in, size_t len, char* out) {
  static const char* hex = "0123456789abcdef";
  for (size_t i = 0; i < len; i++) {
    out[i * 2] = hex[in[i] >> 4];
    out[i * 2 + 1] = hex[in[i] & 0x0F];
  }
  out[len * 2] = '\0';
}

void sha256Hex(const uint8_t* data, size_t len, char out[65]) {
  uint8_t digest[32];
  mbedtls_md(mbedtls_md_info_from_type(MBEDTLS_MD_SHA256), data, len, digest);
  toHex(digest, 32, out);
}

void hmacSha256Hex(const char* key, const String& message, char out[65]) {
  uint8_t digest[32];
  mbedtls_md_hmac(mbedtls_md_info_from_type(MBEDTLS_MD_SHA256),
                  (const uint8_t*)key, strlen(key),
                  (const uint8_t*)message.c_str(), message.length(), digest);
  toHex(digest, 32, out);
}

uint32_t crc32(const uint8_t* data, size_t len) {
  uint32_t crc = 0xFFFFFFFF;
  for (size_t i = 0; i < len; i++) {
    crc ^= data[i];
    for (int b = 0; b < 8; b++) crc = (crc >> 1) ^ (0xEDB88320 & (0 - (crc & 1)));
  }
  return ~crc;
}

// ---------------------------------------------------------------------------
// Kamera lewat Wi-Fi (MODE UJI, devices/esp32cam_wifi)
// ---------------------------------------------------------------------------
#if CAM_LINK_WIFI

String camBaseUrl;   // "http://<ip>" setelah ditemukan

// Tentukan alamat kamera: CAM_HOST, atau cari "methics-cam.local" lewat mDNS.
bool camResolve() {
  if (camBaseUrl.length()) return true;
  if (strlen(CAM_HOST)) {
    camBaseUrl = String("http://") + CAM_HOST;
    return true;
  }
  static bool mdnsStarted = false;
  if (!mdnsStarted) mdnsStarted = MDNS.begin("methics-scanner");
  IPAddress ip = MDNS.queryHost(CAM_MDNS_NAME, 2500);
  if (ip.toString() == "0.0.0.0") {
    Serial.printf("[Cam] %s.local tidak ditemukan; isi CAM_HOST dengan IP kamera\n", CAM_MDNS_NAME);
    return false;
  }
  camBaseUrl = "http://" + ip.toString();
  Serial.printf("[Cam] kamera ditemukan di %s\n", camBaseUrl.c_str());
  return true;
}

// Panggil endpoint kamera. Mengembalikan kode HTTP (< 0 = gagal terhubung).
int camHttp(bool post, const char* path, unsigned long timeoutMs, String& body) {
  body = "";
  if (WiFi.status() != WL_CONNECTED || !camResolve()) return -1;
  HTTPClient http;
  http.setConnectTimeout(2000);
  http.setTimeout(timeoutMs);
  if (!http.begin(camBaseUrl + path)) return -1;
  http.addHeader("X-Cam-Token", CAM_TOKEN);
  int code = post ? http.POST("") : http.GET();
  if (code > 0) body = http.getString();
  http.end();
  if (code < 0 && !strlen(CAM_HOST)) camBaseUrl = "";   // IP kamera mungkin berubah: cari ulang
  return code;
}

String camHttpError(int code, const String& body) {
  if (code < 0) return "Kamera tidak merespons";
  if (code == 401) return "Token kamera salah";
  return "Kamera: " + (body.length() ? body : String("HTTP ") + code);
}

bool camPing() {
  String body;
  int code = camHttp(false, "/ping", 2500, body);
  cameraOk = code == 200 && body.startsWith("PONG");
  if (!cameraOk) Serial.printf("[Cam] ping: %s\n", camHttpError(code, body).c_str());
  return cameraOk;
}

// Nyalakan kamera (stream ke buffer di ESP32-CAM).
bool camOn(String& err) {
  String body;
  int code = camHttp(true, "/on", CAM_ON_TIMEOUT_MS, body);
  if (code == 200) {
    cameraOk = true;
    return true;
  }
  if (code < 0) cameraOk = false;
  err = camHttpError(code, body);
  return false;
}

void camOff() {
  String body;
  camHttp(true, "/off", 1500, body);
}

// Ambil satu frame JPEG. Berhasil -> *out (malloc, wajib free) dan *outLen.
bool camCapture(uint8_t** out, size_t* outLen, String& err) {
  if (WiFi.status() != WL_CONNECTED || !camResolve()) { err = "Kamera tidak merespons"; cameraOk = false; return false; }
  HTTPClient http;
  http.setConnectTimeout(2000);
  http.setTimeout(CAM_TIMEOUT_MS);
  if (!http.begin(camBaseUrl + "/capture")) { err = "Kamera tidak merespons"; return false; }
  http.addHeader("X-Cam-Token", CAM_TOKEN);
  int code = http.GET();
  if (code != 200) {
    String body = code > 0 ? http.getString() : String("");
    http.end();
    err = camHttpError(code, body);
    if (code < 0) {
      cameraOk = false;
      if (!strlen(CAM_HOST)) camBaseUrl = "";
    }
    return false;
  }
  int len = http.getSize();
  if (len <= 0 || len > MAX_IMAGE_BYTES) { http.end(); err = "Ukuran foto tidak valid"; return false; }
  if (ESP.getMaxAllocHeap() < (size_t)len + HEAP_RESERVE) { http.end(); err = "Memori tidak cukup"; return false; }
  uint8_t* buf = (uint8_t*)malloc(len);
  if (!buf) { http.end(); err = "Memori tidak cukup"; return false; }

  WiFiClient* stream = http.getStreamPtr();
  size_t got = 0;
  unsigned long t0 = millis();
  while (got < (size_t)len && millis() - t0 < CAM_TIMEOUT_MS) {
    size_t avail = stream->available();
    if (avail) got += stream->readBytes(buf + got, min(avail, (size_t)len - got));
    else if (!stream->connected()) break;
    else delay(1);
  }
  http.end();
  if (got != (size_t)len) { free(buf); err = "Transfer foto terputus"; return false; }
  cameraOk = true;
  *out = buf;
  *outLen = len;
  return true;
}

#else  // CAM_LINK_WIFI == 0 : kabel UART

// Kamera lewat UART (devices/esp32cam_uart)
HardwareSerial& Cam = Serial2;

// Baca satu baris dari kamera; abaikan "READY" dan log lain. Kosong = timeout.
String camReadLine(unsigned long timeoutMs) {
  String s;
  unsigned long t0 = millis();
  while (millis() - t0 < timeoutMs) {
    while (Cam.available()) {
      char c = (char)Cam.read();
      if (c == '\r') continue;
      if (c == '\n') {
        if (s.length()) return s;
        continue;
      }
      if (s.length() < 64) s += c;
    }
    delay(1);
  }
  return "";
}

void camFlush() {
  while (Cam.available()) Cam.read();
}

bool camPing() {
  camFlush();
  Cam.print("PING\n");
  unsigned long t0 = millis();
  while (millis() - t0 < 1500) {
    String l = camReadLine(1500 - (millis() - t0));
    if (l.startsWith("PONG")) {
      cameraOk = true;
      return true;
    }
    if (l.length() == 0) break;
  }
  cameraOk = false;
  return false;
}

// Kirim perintah satu baris lalu tunggu balasan yang diawali `expect`.
bool camCommand(const char* cmd, const char* expect, unsigned long timeoutMs, String& reply) {
  camFlush();
  Cam.print(cmd);
  Cam.print('\n');
  unsigned long t0 = millis();
  while (millis() - t0 < timeoutMs) {
    reply = camReadLine(timeoutMs - (millis() - t0));
    if (reply.length() == 0) break;
    if (reply.startsWith(expect) || reply.startsWith("ERR")) break;
  }
  bool ok = reply.startsWith(expect);
  if (reply.length() == 0) cameraOk = false;
  return ok;
}

// Nyalakan kamera (stream ke buffer di ESP32-CAM).
bool camOn(String& err) {
  String reply;
  if (camCommand("ON", "ON", CAM_ON_TIMEOUT_MS, reply)) {
    cameraOk = true;
    return true;
  }
  err = reply.length() ? "Kamera: " + reply.substring(reply.indexOf(' ') + 1) : String("Kamera tidak merespons");
  return false;
}

void camOff() {
  String reply;
  camCommand("OFF", "OFF", 1000, reply);
}

// Minta foto. Berhasil -> *out (malloc, wajib free) dan *outLen.
bool camCapture(uint8_t** out, size_t* outLen, String& err) {
  camFlush();
  Cam.print("CAP\n");
  String header;
  unsigned long t0 = millis();
  while (millis() - t0 < CAM_TIMEOUT_MS) {
    header = camReadLine(CAM_TIMEOUT_MS - (millis() - t0));
    if (header.startsWith("IMG ") || header.startsWith("ERR") || header.length() == 0) break;
  }
  if (header.length() == 0) { err = "Kamera tidak merespons"; cameraOk = false; return false; }
  if (header.startsWith("ERR")) { err = "Kamera: " + header.substring(4); return false; }

  unsigned long len = 0, expectCrc = 0;
  if (sscanf(header.c_str(), "IMG %lu %lx", &len, &expectCrc) != 2 || len == 0 || len > MAX_IMAGE_BYTES) {
    err = "Header kamera tidak valid";
    return false;
  }
  if (ESP.getMaxAllocHeap() < len + HEAP_RESERVE) { err = "Memori tidak cukup"; return false; }
  uint8_t* buf = (uint8_t*)malloc(len);
  if (!buf) { err = "Memori tidak cukup"; return false; }

  size_t got = 0;
  t0 = millis();
  while (got < len && millis() - t0 < CAM_TIMEOUT_MS) {
    int n = Cam.read(buf + got, len - got);
    if (n > 0) got += n;
    else delay(1);
  }
  if (got != len) { free(buf); err = "Transfer foto terputus"; return false; }
  if (crc32(buf, len) != expectCrc) { free(buf); err = "Foto rusak (CRC)"; return false; }
  cameraOk = true;
  *out = buf;
  *outLen = len;
  return true;
}

#endif  // CAM_LINK_WIFI

// ---------------------------------------------------------------------------
// HTTP (WiFiClient mentah: foto dikirim bertahap tanpa salinan kedua)
// ---------------------------------------------------------------------------
bool writeAll(WiFiClient& c, const uint8_t* data, size_t len) {
  size_t sent = 0;
  unsigned long t0 = millis();
  while (sent < len && millis() - t0 < HTTP_TIMEOUT_MS) {
    size_t n = c.write(data + sent, min((size_t)2048, len - sent));
    if (n > 0) sent += n;
    else delay(2);
  }
  return sent == len;
}

HttpResult readResponse(WiFiClient& c) {
  HttpResult r;
  unsigned long t0 = millis();
  while (!c.available() && c.connected() && millis() - t0 < HTTP_TIMEOUT_MS) delay(5);
  String statusLine = c.readStringUntil('\n');
  int sp = statusLine.indexOf(' ');
  if (sp < 0) return r;
  r.status = statusLine.substring(sp + 1, sp + 4).toInt();
  long contentLength = -1;
  while (c.connected() || c.available()) {
    String h = c.readStringUntil('\n');
    h.trim();
    if (h.length() == 0) break;
    if (h.substring(0, 15).equalsIgnoreCase("content-length:")) contentLength = h.substring(15).toInt();
  }
  t0 = millis();
  while ((c.connected() || c.available()) && millis() - t0 < HTTP_TIMEOUT_MS) {
    while (c.available() && r.body.length() < 2048) r.body += (char)c.read();
    if (contentLength >= 0 && (long)r.body.length() >= contentLength) break;
    if (r.body.length() >= 2048) break;
    delay(2);
  }
  return r;
}

// Ambil nilai string JSON sederhana: "key":"nilai". Kosong bila null/tidak ada.
String jsonString(const String& body, const char* key) {
  String pat = String("\"") + key + "\":";
  int i = body.indexOf(pat);
  if (i < 0) return "";
  i += pat.length();
  while (i < (int)body.length() && body[i] == ' ') i++;
  if (i >= (int)body.length() || body[i] != '"') return "";
  String out;
  for (i++; i < (int)body.length() && body[i] != '"'; i++) {
    if (body[i] == '\\' && i + 1 < (int)body.length()) {
      char e = body[++i];
      if (e == 'u' && i + 4 < (int)body.length()) {
        long cp = strtol(body.substring(i + 1, i + 5).c_str(), nullptr, 16);
        out += cp < 128 ? (char)cp : '?';
        i += 4;
      } else if (e == 'n' || e == 't') {
        out += ' ';
      } else {
        out += e;
      }
    } else {
      out += body[i];
    }
  }
  return out;
}

void sendPing() {
  if (WiFi.status() != WL_CONNECTED) {
    WiFi.reconnect();
    return;
  }
  WiFiClient c;
  c.setTimeout(HTTP_TIMEOUT_MS / 1000);
  if (!c.connect(SERVER_HOST, SERVER_PORT)) {
    Serial.println("[Ping] server tidak terjangkau");
    return;
  }
  String body = String("device_id=") + DEVICE_ID;
  c.printf("POST /api/v1/devices/ping HTTP/1.1\r\nHost: %s:%d\r\n", SERVER_HOST, SERVER_PORT);
  c.printf("Authorization: Bearer %s\r\nX-Device-Id: %s\r\n", DEVICE_TOKEN, DEVICE_ID);
  c.printf("Content-Type: application/x-www-form-urlencoded\r\nContent-Length: %u\r\nConnection: close\r\n\r\n", body.length());
  c.print(body);
  HttpResult r = readResponse(c);
  c.stop();
  Serial.printf("[Ping] HTTP %d\n", r.status);
}

void freeFrames(Frames& f) {
  for (int i = 0; i < f.count; i++) free(f.data[i]);
  f.count = 0;
}

// SHA-256 atas semua frame digabung berurutan (sama dengan framesHash di Go).
void framesSha256Hex(const Frames& f, char out[65]) {
  uint8_t digest[32];
  mbedtls_md_context_t ctx;
  mbedtls_md_init(&ctx);
  mbedtls_md_setup(&ctx, mbedtls_md_info_from_type(MBEDTLS_MD_SHA256), 0);
  mbedtls_md_starts(&ctx);
  for (int i = 0; i < f.count; i++) mbedtls_md_update(&ctx, f.data[i], f.len[i]);
  mbedtls_md_finish(&ctx, digest);
  mbedtls_md_free(&ctx);
  toHex(digest, 32, out);
}

HttpResult postScan(const Frames& frames, const char* timestamp) {
  const char* liveness = LIVENESS_PASSED ? "true" : "false";

  // Tanda tangan: 6 baris digabung "\n" (lihat API.md / docs/devices.md §4).
  // Baris terakhir = SHA-256 semua frame; CLASS_ID boleh kosong (kelas otomatis).
  char framesHash[65], signature[65];
  framesSha256Hex(frames, framesHash);
  String message = String(timestamp) + "\n" + DEVICE_ID + "\n" + CLASS_ID + "\n" +
                   timestamp + "\n" + liveness + "\n" + framesHash;
  hmacSha256Hex(DEVICE_TOKEN, message, signature);

  const char* B = "----methicsScanBoundary7d9f";
  String head;
  auto field = [&](const char* name, const char* value) {
    head += String("--") + B + "\r\nContent-Disposition: form-data; name=\"" + name + "\"\r\n\r\n" + value + "\r\n";
  };
  field("device_id", DEVICE_ID);
  field("class_id", CLASS_ID);
  field("captured_at", timestamp);
  field("liveness_passed", liveness);
  auto partHead = [&](int i) {
    return String("--") + B + "\r\nContent-Disposition: form-data; name=\"image\"; filename=\"frame-" + (i + 1) +
           ".jpg\"\r\nContent-Type: image/jpeg\r\n\r\n";
  };
  String tail = String("--") + B + "--\r\n";
  size_t contentLength = head.length() + tail.length();
  for (int i = 0; i < frames.count; i++) contentLength += partHead(i).length() + frames.len[i] + 2;

  HttpResult r;
  WiFiClient c;
  c.setTimeout(HTTP_TIMEOUT_MS / 1000);
  if (!c.connect(SERVER_HOST, SERVER_PORT)) return r;
  c.printf("POST /api/v1/scans HTTP/1.1\r\nHost: %s:%d\r\n", SERVER_HOST, SERVER_PORT);
  c.printf("Authorization: Bearer %s\r\n", DEVICE_TOKEN);
  c.printf("X-Device-Timestamp: %s\r\nX-Device-Signature: %s\r\n", timestamp, signature);
  c.printf("Content-Type: multipart/form-data; boundary=%s\r\n", B);
  c.printf("Content-Length: %u\r\nConnection: close\r\n\r\n", (unsigned)contentLength);
  bool ok = writeAll(c, (const uint8_t*)head.c_str(), head.length());
  for (int i = 0; ok && i < frames.count; i++) {
    String ph = partHead(i);
    ok = writeAll(c, (const uint8_t*)ph.c_str(), ph.length()) &&
         writeAll(c, frames.data[i], frames.len[i]) &&
         writeAll(c, (const uint8_t*)"\r\n", 2);
  }
  ok = ok && writeAll(c, (const uint8_t*)tail.c_str(), tail.length());
  if (ok) r = readResponse(c);
  c.stop();
  return r;
}

// ---------------------------------------------------------------------------
// Alur scan
// ---------------------------------------------------------------------------
void showError(const String& line1, const String& line2) {
  showMessage("GAGAL", TFT_RED, line1, line2);
  screenFor(RESULT_SHOW_MS);
  beepBlocking(3, 90, 90);
}

// Garis progres di bawah judul selama jendela scan.
void drawProgress(int done, int total) {
  int w = tft.width() - 60, x = 30, y = tft.height() / 2 + 30;
  tft.drawRect(x, y, w, 10, TFT_DARKGREY);
  tft.fillRect(x + 1, y + 1, (w - 2) * done / total, 8, TFT_CYAN);
}

// Ambil frame selama SCAN_WINDOW_MS (kamera sudah ON). Frame disebar merata
// agar wajah terlihat di beberapa momen berbeda.
void captureWindow(Frames& frames, String& lastErr) {
  unsigned long start = millis();
  unsigned long gap = SCAN_WINDOW_MS / SCAN_MAX_FRAMES;
  for (int i = 0; i < SCAN_MAX_FRAMES; i++) {
    unsigned long due = start + i * gap;
    while ((long)(millis() - due) < 0) { updateBeep(); delay(2); }
    if (millis() - start > SCAN_WINDOW_MS + 1500) break;   // UART lambat: jangan memperpanjang scan
    uint8_t* image = nullptr;
    size_t imageLen = 0;
    if (camCapture(&image, &imageLen, lastErr)) {
      frames.data[frames.count] = image;
      frames.len[frames.count] = imageLen;
      frames.count++;
      drawProgress(i + 1, SCAN_MAX_FRAMES);
      Serial.printf("[Scan] frame %d: %u byte (t=%lu ms)\n", frames.count, (unsigned)imageLen, millis() - start);
    } else {
      Serial.printf("[Scan] frame %d gagal: %s\n", i + 1, lastErr.c_str());
      if (lastErr == "Memori tidak cukup" || lastErr == "Kamera tidak merespons" || lastErr == "Token kamera salah") break;
    }
  }
}

void showResult(const HttpResult& r) {
  String result = jsonString(r.body, "result");
  String status = jsonString(r.body, "status");
  String name = jsonString(r.body, "name");
  String nim = jsonString(r.body, "nim");
  String kelas = jsonString(r.body, "classId");
  String reason = jsonString(r.body, "reviewReason");
  if (result.length() == 0) {   // server lama tanpa field result
    result = status == "Hadir" ? "hadir" : status == "Perlu ditinjau" ? "perlu_ditinjau" : "tidak_terdaftar";
  }
  String detail = nim.length() ? "NIM " + nim + (kelas.length() ? "  -  " + kelas : String("")) : String("");

  if (result == "hadir") {
    showMessage("HADIR", TFT_GREEN, name, detail);
    beepBlocking(1, 450, 0);
  } else if (result == "tidak_terdaftar") {
    showMessage("Wajah tidak Terdaftar", TFT_RED, "Hubungi admin untuk enroll", "");
    beepBlocking(3, 90, 90);
  } else if (result == "wajah_tidak_terdeteksi") {
    showMessage("Wajah tidak terdeteksi", COLOR_ORANGE, "Hadapkan wajah ke kamera", "lalu coba lagi.");
    beepBlocking(2, 90, 90);
  } else {
    showMessage("DITINJAU", COLOR_ORANGE, name.length() ? name : String("Perlu ditinjau"), reason);
    beepBlocking(2, 120, 120);
  }
  screenFor(RESULT_SHOW_MS);
}

void doScan() {
  lastScan = millis();
  screenFor(RESULT_SHOW_MS + 15000);
  showMessage("Kamera menyala", TFT_CYAN, "Lihat ke kamera", "");
  beepBlocking(1, 80, 0);

  if (WiFi.status() != WL_CONNECTED && !connectWiFi(8000)) {
    showError("Wi-Fi terputus", "Periksa jaringan Wi-Fi dan sinyal di ruangan.");
    return;
  }
  if (!timeOk && !syncTime(5000)) {
    showError("Jam belum sinkron", "NTP tidak terjangkau; server menolak scan tanpa jam yang benar.");
    return;
  }

  String err;
  unsigned long t0 = millis();
  if (!camOn(err)) {
    Serial.printf("[Scan] kamera: %s\n", err.c_str());
    showError(err, CAM_LINK_WIFI ? "Periksa Wi-Fi, CAM_HOST/CAM_TOKEN & daya ESP32-CAM." : "Periksa kabel UART & daya ESP32-CAM.");
    return;
  }
  Serial.printf("[Scan] kamera ON dalam %lu ms\n", millis() - t0);
  showMessage("Memindai...", TFT_CYAN, "Tahan wajah +-2 detik", "");
  Frames frames;
  captureWindow(frames, err);
  camOff();

  if (frames.count == 0) {
    showError(err.length() ? err : String("Tidak ada frame"),
              CAM_LINK_WIFI ? "Periksa Wi-Fi, CAM_HOST/CAM_TOKEN & daya ESP32-CAM." : "Periksa kabel UART & daya ESP32-CAM.");
    return;
  }
  showMessage("Memverifikasi...", TFT_CYAN, String(frames.count) + " frame", "");
  char ts[25];
  rfc3339Now(ts, sizeof(ts));
  t0 = millis();
  HttpResult r = postScan(frames, ts);
  freeFrames(frames);
  Serial.printf("[Scan] HTTP %d dalam %lu ms: %s\n", r.status, millis() - t0, r.body.c_str());

  if (r.status == 202) {
    showResult(r);
    return;
  }
  if (r.status < 0) {
    showError("Server tidak terjangkau", String(SERVER_HOST) + ":" + SERVER_PORT);
    return;
  }
  String code = jsonString(r.body, "code");
  String message = jsonString(r.body, "message");
  if (code == "invalid_signature" || code == "stale_capture") timeOk = false;   // sinkron ulang jam
  showError(String("HTTP ") + r.status + (code.length() ? " " + code : ""), message);
}

// ---------------------------------------------------------------------------
void printStatus() {
  char ts[25];
  rfc3339Now(ts, sizeof(ts));
  Serial.printf("[Status] WiFi=%s IP=%s jam=%s (%s) kamera=%s heap=%u maxblok=%u\n",
                WiFi.status() == WL_CONNECTED ? "OK" : "putus", WiFi.localIP().toString().c_str(),
                ts, timeOk ? "sinkron" : "BELUM", cameraOk ? "OK" : "tidak", (unsigned)ESP.getFreeHeap(), (unsigned)ESP.getMaxAllocHeap());
}

void handleSerialCommand() {
  if (!Serial.available()) return;
  String cmd = Serial.readStringUntil('\n');
  cmd.trim();
  if (cmd == "scan") doScan();
  else if (cmd == "ping") sendPing();
  else if (cmd == "cam") Serial.printf("[Cam] %s\n", camPing() ? "PONG" : "tidak merespons");
  else if (cmd == "camon") { String e; Serial.printf("[Cam] ON %s\n", camOn(e) ? "OK" : e.c_str()); }
  else if (cmd == "camoff") { camOff(); Serial.println("[Cam] OFF"); }
  else if (cmd == "status") printStatus();
}

void setup() {
  Serial.begin(115200);
  pinMode(TRIG_PIN, OUTPUT);
  pinMode(ECHO_PIN, INPUT);
  digitalWrite(TRIG_PIN, LOW);
  buzzerInit();
  buzzerWrite(false);
  pinMode(TFT_BL, OUTPUT);
  tft.init();
  tft.setRotation(ROT);

#if !CAM_LINK_WIFI
  Cam.setRxBufferSize(8192);
  Cam.begin(CAM_BAUD, SERIAL_8N1, CAM_RX_PIN, CAM_TX_PIN);
#endif

  setBacklight(true);
  showMessage("Memulai", TFT_CYAN, "Wi-Fi...", "");
  bool wifi = connectWiFi(20000);
  showMessage("Memulai", TFT_CYAN, wifi ? "Wi-Fi OK" : "Wi-Fi GAGAL", "Sinkron jam (NTP)...");
  if (wifi) syncTime(10000);
  delay(1500);  // beri waktu ESP32-CAM selesai boot (uji kamera lalu power-down)
  camPing();
#if CAM_LINK_WIFI
  Serial.printf("[Cam] mode Wi-Fi: %s\n", cameraOk ? camBaseUrl.c_str() : "kamera belum terhubung");
#endif
  showMessage(wifi && timeOk && cameraOk ? "Siap" : "Periksa", wifi && timeOk && cameraOk ? TFT_GREEN : COLOR_ORANGE,
              String(DEVICE_ID),
              String("WiFi ") + (wifi ? "OK" : "GAGAL") + ", jam " + (timeOk ? "OK" : "GAGAL") +
              ", kamera " + (cameraOk ? "OK" : "GAGAL"));
  printStatus();
  if (wifi) sendPing();
  lastPing = millis();
  screenFor(4000);
}

void loop() {
  unsigned long now = millis();
  handleSerialCommand();

  if (now - lastMeasure >= MEASURE_INTERVAL) {
    lastMeasure = now;
    float dist = readDistanceCm();
    bool near = dist > 0 && dist <= DETECT_CM;
    if (near) {
      if (hits < 255) hits++;
      awayCount = 0;
    } else {
      hits = 0;
      if (awayCount < 255) awayCount++;
      if (awayCount >= 5) armed = true;   // ~0,5 detik tanpa objek
    }
    if (hits >= CONFIRM_HITS && armed && now - lastScan >= SCAN_COOLDOWN_MS) {
      armed = false;
      doScan();
      now = millis();
    }
  }

  if (now - lastPing >= PING_INTERVAL_MS) {
    lastPing = now;
    sendPing();
  }

  updateBeep();
  if (screenOn && (long)(now - screenOffAt) >= 0) setBacklight(false);
}
