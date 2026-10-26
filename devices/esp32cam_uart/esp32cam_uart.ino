/*
 * ==============================================================================
 * Project : Algorithmics Face Recognition (methics)
 * Board   : AI Thinker ESP32-CAM   (Tools > Board > "AI Thinker ESP32-CAM")
 * Peran   : Kamera untuk ESP32 utama (devices/esp32_scanner). TANPA Wi-Fi.
 *
 * ESP32 utama meminta foto lewat UART, kamera membalas dengan JPEG.
 * Foto tidak pernah dibuka di jaringan; yang mengirim ke server hanya
 * ESP32 utama (dengan token + tanda tangan HMAC).
 *
 * Wiring (3 kabel + daya):
 *   ESP32-CAM GPIO15 (TX1)  ->  ESP32 utama GPIO16 (RX2)
 *   ESP32-CAM GPIO14 (RX1)  <-  ESP32 utama GPIO17 (TX2)
 *   ESP32-CAM GND           --  ESP32 utama GND   (GND WAJIB disatukan)
 *   ESP32-CAM 5V            <-  catu 5V >= 2 A (jangan dari pin 3V3 ESP32 utama)
 *
 * UART0 (GPIO1/GPIO3, USB-serial programmer) tetap dipakai untuk upload dan
 * log debug 115200, sehingga kabel ke ESP32 utama tidak perlu dicabut saat
 * flashing. GPIO14/15 adalah pin kartu SD; jangan pasang kartu SD.
 *
 * Protokol (teks + biner, satu permintaan per waktu):
 *   ESP32 utama -> kamera : "PING\n"          kamera -> "PONG psram=<0|1>\n"
 *   ESP32 utama -> kamera : "CAP\n"           kamera -> "IMG <panjang> <crc32hex>\n"
 *                                                       lalu <panjang> byte JPEG
 *                                         atau kamera -> "ERR <alasan>\n"
 *   Saat start kamera mengirim "READY\n".
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

#define FRAME_SIZE       FRAMESIZE_VGA   // 640x480; turunkan ke FRAMESIZE_QVGA bila tanpa PSRAM/terlalu lambat
#define JPEG_QUALITY     12              // 4..63, kecil = lebih bagus & lebih besar
#define JPEG_QUALITY_FALLBACK 20         // dipakai bila hasil melebihi MAX_JPEG_BYTES
#define MAX_JPEG_BYTES   60000           // SAMAKAN dengan MAX_IMAGE_BYTES di ESP32 utama

#define USE_FLASH        0               // 1 = nyalakan LED flash (GPIO4) saat memotret
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
bool cameraReady = false;
bool hasPsram = false;

// CRC-32 IEEE (polinom terbalik 0xEDB88320), sama dengan zlib.crc32.
uint32_t crc32(const uint8_t* data, size_t len) {
  uint32_t crc = 0xFFFFFFFF;
  for (size_t i = 0; i < len; i++) {
    crc ^= data[i];
    for (int b = 0; b < 8; b++) crc = (crc >> 1) ^ (0xEDB88320 & (0 - (crc & 1)));
  }
  return ~crc;
}

bool initCamera() {
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

  hasPsram = psramFound();
  if (hasPsram) {
    config.frame_size = FRAME_SIZE;
    config.fb_count = 2;
    config.fb_location = CAMERA_FB_IN_PSRAM;
    config.grab_mode = CAMERA_GRAB_LATEST;      // selalu frame terbaru
  } else {
    config.frame_size = FRAMESIZE_QVGA;         // tanpa PSRAM: 320x240
    config.fb_count = 1;
    config.fb_location = CAMERA_FB_IN_DRAM;
    config.grab_mode = CAMERA_GRAB_WHEN_EMPTY;
  }

  esp_err_t err = esp_camera_init(&config);
  if (err != ESP_OK) {
    LOGF("[CAM] init gagal 0x%x\n", err);
    return false;
  }
  sensor_t* s = esp_camera_sensor_get();
  s->set_quality(s, JPEG_QUALITY);
  LOGF("[CAM] siap, PSRAM=%d\n", hasPsram);
  return true;
}

// Ambil frame segar: frame pertama bisa berasal dari sebelum permintaan.
camera_fb_t* captureFresh() {
  camera_fb_t* fb = esp_camera_fb_get();
  if (fb) esp_camera_fb_return(fb);
  return esp_camera_fb_get();
}

void handleCapture() {
  if (!cameraReady) {
    Link.print("ERR camera_init\n");
    return;
  }
#if USE_FLASH
  digitalWrite(FLASH_LED_PIN, HIGH);
  delay(80);
#endif
  camera_fb_t* fb = captureFresh();
  sensor_t* s = esp_camera_sensor_get();
  if (fb && fb->len > MAX_JPEG_BYTES) {
    // Terlalu besar untuk buffer ESP32 utama: ulangi dengan kualitas lebih rendah.
    esp_camera_fb_return(fb);
    s->set_quality(s, JPEG_QUALITY_FALLBACK);
    fb = captureFresh();
    s->set_quality(s, JPEG_QUALITY);
  }
#if USE_FLASH
  digitalWrite(FLASH_LED_PIN, LOW);
#endif
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
  digitalWrite(FLASH_LED_PIN, LOW);
#endif
  Link.setTxBufferSize(4096);
  Link.begin(LINK_BAUD, SERIAL_8N1, LINK_RX_PIN, LINK_TX_PIN);

  cameraReady = initCamera();
  Link.print(cameraReady ? "READY\n" : "ERR camera_init\n");
  LOGF("[CAM] UART1 %d baud, RX=%d TX=%d\n", LINK_BAUD, LINK_RX_PIN, LINK_TX_PIN);
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
    if (line == "CAP") {
      handleCapture();
    } else if (line == "PING") {
      Link.printf("PONG psram=%d\n", hasPsram ? 1 : 0);
    } else if (line.length()) {
      Link.print("ERR unknown_command\n");
    }
    line = "";
  }
  delay(2);
}
