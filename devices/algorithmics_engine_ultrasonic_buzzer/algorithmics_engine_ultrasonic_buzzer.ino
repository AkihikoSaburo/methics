/*
  ESP32 + TFT 2.4" ILI9341 (TFT_eSPI) + sensor ultrasonic HC-SR04 + buzzer

  Cara kerja:
    - Saat standby, backlight layar mati dan buzzer diam.
    - Jika ada objek dalam jarak DETECT_CM, backlight menyala DAN buzzer berbunyi
      (BEEP_COUNT kali bip pendek).
    - Layar mati lagi ON_TIME_MS (15 detik) setelah objek terakhir terdeteksi.
      Buzzer hanya berbunyi saat layar berpindah dari mati ke nyala,
      jadi tidak terus berbunyi selama objek masih berada di depan sensor.

  Wiring sensor HC-SR04:
    VCC -> 5V (VIN), GND -> GND, TRIG -> GPIO 26
    ECHO -> GPIO 27 (idealnya lewat pembagi tegangan 1k + 2k)

  Wiring buzzer:
    Buzzer (+) / pin I/O -> GPIO 22
    Buzzer (-) / GND     -> GND
    Modul buzzer 3 pin   -> VCC ke 3V3 (atau 5V sesuai modul)

  Memakai User_Setup.h yang sama seperti sketch sebelumnya.
*/

#include <SPI.h>
#include <TFT_eSPI.h>

#define ROT 2                      // 1 = landscape (320 x 240), 3 = terbalik

// ---- Sensor ultrasonic ----
#define TRIG_PIN 26
#define ECHO_PIN 27

#define DETECT_CM           20     // jarak deteksi (cm)
#define ON_TIME_MS       5000UL   // lama layar menyala setelah deteksi terakhir
#define MEASURE_INTERVAL   100UL   // jeda antar pengukuran (ms)
#define CONFIRM_HITS         2     // deteksi berturut-turut agar dianggap valid

// ---- Buzzer ----
#define BUZZER_PIN       22
#define BUZZER_PASSIVE    0        // 0 = buzzer aktif (bunyi saat diberi tegangan)
                                   // 1 = buzzer pasif (butuh sinyal frekuensi)
#define BUZZER_ON_LEVEL  HIGH      // modul buzzer "low trigger": ganti ke LOW
#define BUZZER_FREQ      2000      // frekuensi nada (hanya untuk buzzer pasif)
#define BUZZER_CH        0         // channel LEDC (hanya ESP32 core 2.x, buzzer pasif)

#define BEEP_COUNT   5             // jumlah bip
#define BEEP_ON_MS   120           // lama tiap bip
#define BEEP_OFF_MS  100           // jeda antar bip

#define DEBUG_SERIAL 1

#ifndef TFT_BL
#define TFT_BL 21
#endif
#ifndef TFT_BACKLIGHT_ON
#define TFT_BACKLIGHT_ON HIGH
#endif

TFT_eSPI tft = TFT_eSPI();

bool screenOn = false;
unsigned long lastDetect = 0;
unsigned long lastMeasure = 0;
uint8_t hits = 0;

// Status bip (non-blocking)
uint8_t beepsLeft = 0;
bool beepOn = false;
unsigned long beepTimer = 0;

// ---------------------------------------------------------------------------
// Buzzer
// ---------------------------------------------------------------------------
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

void startBeep(unsigned long now) {
  beepsLeft = BEEP_COUNT;
  beepOn = false;
  beepTimer = now - BEEP_OFF_MS;   // bip pertama langsung berbunyi
}

void updateBeep(unsigned long now) {
  if (beepsLeft == 0 && !beepOn) return;

  if (!beepOn) {
    if (now - beepTimer >= BEEP_OFF_MS) {
      buzzerWrite(true);
      beepOn = true;
      beepTimer = now;
    }
  } else if (now - beepTimer >= BEEP_ON_MS) {
    buzzerWrite(false);
    beepOn = false;
    beepTimer = now;
    if (beepsLeft > 0) beepsLeft--;
  }
}

// ---------------------------------------------------------------------------
// Layar dan sensor
// ---------------------------------------------------------------------------
void setBacklight(bool on) {
  digitalWrite(TFT_BL, on ? TFT_BACKLIGHT_ON : !TFT_BACKLIGHT_ON);
  screenOn = on;
}

// Mengembalikan jarak dalam cm, atau -1 jika tidak ada pantulan
float readDistanceCm() {
  digitalWrite(TRIG_PIN, LOW);
  delayMicroseconds(2);
  digitalWrite(TRIG_PIN, HIGH);
  delayMicroseconds(10);
  digitalWrite(TRIG_PIN, LOW);

  unsigned long duration = pulseIn(ECHO_PIN, HIGH, 30000UL);  // timeout 30 ms
  if (duration == 0) return -1;
  return duration * 0.0343f / 2.0f;
}

void drawScreen() {
  int W = tft.width();
  int H = tft.height();

  tft.fillScreen(TFT_BLACK);
  tft.setFreeFont(&FreeSansBold18pt7b);   // jika terlalu lebar, pakai FreeSansBold12pt7b
  tft.setTextDatum(MC_DATUM);

  tft.setTextColor(TFT_CYAN, TFT_BLACK);
  tft.drawString("Algorithmics", W / 2, H / 2 - 30);

  tft.setTextColor(TFT_WHITE, TFT_BLACK);
  tft.drawString("Engine", W / 2, H / 2 + 20);

  tft.drawFastHLine(W / 2 - 80, H / 2 + 55, 160, TFT_CYAN);
}

// ---------------------------------------------------------------------------
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

  setBacklight(false);   // matikan dulu sebelum menggambar agar tidak berkedip
  drawScreen();          // konten tetap di layar, hanya backlight yang diatur
}

void loop() {
  unsigned long now = millis();

  if (now - lastMeasure >= MEASURE_INTERVAL) {
    lastMeasure = now;

    float dist = readDistanceCm();

#if DEBUG_SERIAL
    if (dist > 0) Serial.printf("Jarak: %.1f cm\n", dist);
#endif

    if (dist > 0 && dist <= DETECT_CM) {
      if (hits < 255) hits++;
    } else {
      hits = 0;
    }

    if (hits >= CONFIRM_HITS) {
      lastDetect = now;
      if (!screenOn) {
        setBacklight(true);
        startBeep(now);       // buzzer berbunyi bersamaan dengan layar menyala
#if DEBUG_SERIAL
        Serial.println("Objek terdeteksi -> layar ON + buzzer");
#endif
      }
    }
  }

  updateBeep(now);

  if (screenOn && (now - lastDetect >= ON_TIME_MS)) {
    setBacklight(false);
#if DEBUG_SERIAL
    Serial.println("Timeout -> layar OFF");
#endif
  }
}
