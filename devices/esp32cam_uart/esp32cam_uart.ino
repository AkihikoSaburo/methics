/*
 * ==============================================================================
 * Project : Algorithmics Face Recognition (methics)
 * Board   : AI Thinker ESP32-CAM   (Tools > Board > "AI Thinker ESP32-CAM")
 * Peran   : Kamera untuk ESP32 utama (devices/esp32_scanner). TANPA Wi-Fi.
 *
 * Alur:
 *   - Saat idle sensor kamera dimatikan (power-down lewat pin PWDN) supaya
 *     tidak panas dan hemat daya.
 *   - Begitu sensor ultrasonik di ESP32 utama mendeteksi orang, ESP32 utama
 *     mengirim "ON": kamera menyala dan terus menangkap frame (stream) ke
 *     buffer PSRAM, sehingga setiap "CAP" langsung mendapat frame terbaru.
 *   - ESP32 utama mengambil beberapa frame selama ±2 detik, lalu mengirim
 *     "OFF". Kamera juga mati sendiri bila tidak ada perintah selama
 *     IDLE_OFF_MS.
 * Foto tidak pernah dibuka di jaringan; yang mengirim ke server hanya ESP32
 * utama (dengan token + tanda tangan HMAC).
 *
 * Wiring (3 kabel + daya, sama seperti sebelumnya):
 *   ESP32-CAM GPIO15 (TX1)  ->  ESP32 utama GPIO16 (RX2)
 *   ESP32-CAM GPIO14 (RX1)  <-  ESP32 utama GPIO17 (TX2)
 *   ESP32-CAM GND           --  ESP32 utama GND   (GND WAJIB disatukan)
 *   ESP32-CAM 5V            <-  catu 5V >= 2 A (jangan dari pin 3V3 ESP32 utama)
 *
 * UART0 (GPIO1/GPIO3, USB-serial programmer) tetap dipakai untuk upload dan
 * log debug 115200, sehingga kabel ke ESP32 utama tidak perlu dicabut saat
 * flashing. GPIO14/15 adalah pin kartu SD; jangan pasang kartu SD.
 *
 * Protokol (baris teks diakhiri "\n"; satu permintaan per waktu):
 *   "PING" -> "PONG psram=<0|1> cam=<on|off>"
 *   "ON"   -> "ON"   (kamera menyala + pemanasan exposure)  atau "ERR camera_init"
 *   "CAP"  -> "IMG <panjang> <crc32hex>" lalu <panjang> byte JPEG
 *             atau "ERR <alasan>". Bila kamera mati, CAP menyalakannya dulu.
 *   "OFF"  -> "OFF"  (kamera dimatikan)
 *   Saat start kamera mengirim "READY".
 * CRC32 = CRC-32 IEEE (sama dengan zlib.crc32) atas byte JPEG.
 * ==============================================================================
 */

#include "esp_camera.h"

// ---------------------------------------------------------------------------
// Konfigurasi
// ---------------------------------------------------------------------------
#define LINK_BAUD        460800   // SAMAKAN dengan CAM_BAUD di ESP32 utama
#define LINK_RX_PIN      14
#define LINK_TX_PIN      15

// Frame kecil agar beberapa frame muat di RAM ESP32 utama dan cepat lewat
// UART (QVGA ±8–15 KB ≈ 0,2–0,3 detik per frame pada 460800 baud). Wajah
// pada jarak <= 20 cm tetap cukup besar untuk pengenalan.
#define FRAME_SIZE       FRAMESIZE_QVGA  // 320x240
#define JPEG_QUALITY     10              // 4..63, kecil = lebih bagus & lebih besar
#define JPEG_QUALITY_FALLBACK 18         // dipakai bila hasil melebihi MAX_JPEG_BYTES
#define MAX_JPEG_BYTES   24000           // SAMAKAN dengan MAX_IMAGE_BYTES di ESP32 utama

#define WARMUP_FRAMES    4               // frame awal dibuang agar exposure/white balance stabil
#define IDLE_OFF_MS      15000UL         // kamera mati sendiri bila tidak ada perintah

#define USE_FLASH        0               // 1 = LED flash (GPIO4) menyala selama kamera ON
#define FLASH_LED_PIN    4
#define DEBUG_LOG        1               // log ke Serial (UART0) 115200

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

#if DEBUG_LOG
#define LOGF(...) Serial.printf(__VA_ARGS__)
#else
#define LOGF(...)
#endif

HardwareSerial Link(1);   // UART1 ke ESP32 utama
bool cameraOn = false;
bool hasPsram = false;
unsigned long lastCommand = 0;

// CRC-32 IEEE (polinom terbalik 0xEDB88320), sama dengan zlib.crc32.
uint32_t crc32(const uint8_t* data, size_t len) {
  uint32_t crc = 0xFFFFFFFF;
  for (size_t i = 0; i < len; i++) {
    crc ^= data[i];
    for (int b = 0; b < 8; b++) crc = (crc >> 1) ^ (0xEDB88320 & (0 - (crc & 1)));
  }
  return ~crc;
}

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
    LOGF("[CAM] OFF\n");
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
    config.grab_mode = CAMERA_GRAB_LATEST;      // CAP selalu dapat frame terbaru
  } else {
    config.fb_count = 1;
    config.fb_location = CAMERA_FB_IN_DRAM;
    config.grab_mode = CAMERA_GRAB_WHEN_EMPTY;
  }

  unsigned long t0 = millis();
  esp_err_t err = esp_camera_init(&config);
  if (err != ESP_OK) {
    LOGF("[CAM] init gagal 0x%x\n", err);
    cameraOff();
    return false;
  }
  sensor_t* s = esp_camera_sensor_get();
  s->set_quality(s, JPEG_QUALITY);
  flash(true);
  // Buang beberapa frame awal: exposure dan white balance OV2640 butuh waktu.
  for (int i = 0; i < WARMUP_FRAMES; i++) {
    camera_fb_t* fb = esp_camera_fb_get();
    if (fb) esp_camera_fb_return(fb);
  }
  cameraOn = true;
  LOGF("[CAM] ON dalam %lu ms (PSRAM=%d)\n", millis() - t0, hasPsram);
  return true;
}

camera_fb_t* grabFrame() {
  camera_fb_t* fb = esp_camera_fb_get();
  if (!hasPsram && fb) {
    // Tanpa PSRAM (1 buffer, GRAB_WHEN_EMPTY) frame pertama bisa basi.
    esp_camera_fb_return(fb);
    fb = esp_camera_fb_get();
  }
  return fb;
}

void handleCapture() {
  if (!cameraOnNow()) {
    Link.print("ERR camera_init\n");
    return;
  }
  camera_fb_t* fb = grabFrame();
  if (fb && fb->len > MAX_JPEG_BYTES) {
    // Terlalu besar untuk buffer ESP32 utama: ulangi dengan kualitas lebih rendah.
    sensor_t* s = esp_camera_sensor_get();
    esp_camera_fb_return(fb);
    s->set_quality(s, JPEG_QUALITY_FALLBACK);
    fb = grabFrame();
    s->set_quality(s, JPEG_QUALITY);
  }
  if (!fb) {
    Link.print("ERR capture\n");
    LOGF("[CAM] capture gagal\n");
    return;
  }
  if (fb->len > MAX_JPEG_BYTES) {
    Link.printf("ERR too_big %u\n", (unsigned)fb->len);
    esp_camera_fb_return(fb);
    return;
  }
  uint32_t crc = crc32(fb->buf, fb->len);
  Link.printf("IMG %u %08lx\n", (unsigned)fb->len, (unsigned long)crc);
  Link.write(fb->buf, fb->len);
  Link.flush();
  LOGF("[CAM] kirim %u byte %ux%u crc=%08lx\n", (unsigned)fb->len, fb->width, fb->height, (unsigned long)crc);
  esp_camera_fb_return(fb);
}

String line;

void setup() {
  Serial.begin(115200);
#if USE_FLASH
  pinMode(FLASH_LED_PIN, OUTPUT);
#endif
  hasPsram = psramFound();
  Link.setTxBufferSize(4096);
  Link.begin(LINK_BAUD, SERIAL_8N1, LINK_RX_PIN, LINK_TX_PIN);

  // Uji sekali bahwa kamera bisa menyala, lalu matikan sampai ada orang.
  bool ok = cameraOnNow();
  cameraOff();
  Link.print(ok ? "READY\n" : "ERR camera_init\n");
  LOGF("[CAM] UART1 %d baud, RX=%d TX=%d, kamera %s (idle: mati)\n", LINK_BAUD, LINK_RX_PIN, LINK_TX_PIN, ok ? "OK" : "GAGAL");
}

void loop() {
  while (Link.available()) {
    char c = (char)Link.read();
    if (c == '\r') continue;
    if (c != '\n') {
      if (line.length() < 32) line += c;   // abaikan sampah panjang
      continue;
    }
    line.trim();
    lastCommand = millis();
    if (line == "CAP") {
      handleCapture();
    } else if (line == "ON") {
      Link.print(cameraOnNow() ? "ON\n" : "ERR camera_init\n");
    } else if (line == "OFF") {
      cameraOff();
      Link.print("OFF\n");
    } else if (line == "PING") {
      Link.printf("PONG psram=%d cam=%s\n", hasPsram ? 1 : 0, cameraOn ? "on" : "off");
    } else if (line.length()) {
      Link.print("ERR unknown_command\n");
    }
    line = "";
  }
  if (cameraOn && millis() - lastCommand >= IDLE_OFF_MS) cameraOff();   // pengaman
  delay(2);
}
