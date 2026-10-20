/*
 * ==============================================================================
 * Project   : Algorithmics Face Recognation (Methics)
 * Board     : ESP8266 (NodeMCU / Wemos D1 Mini / Generic ESP8266)
 * Deskripsi : Pengujian koneksi dan heartbeat ping dari ESP8266 ke server backend
 *             untuk memperbarui status perangkat menjadi "Terhubung" di dashboard.
 * Server IP : 10.229.23.195:8080
 * ==============================================================================
 */

#include <ESP8266WiFi.h>
#include <ESP8266HTTPClient.h>
#include <WiFiClient.h>

// -----------------------------------------------------------------------------
// KONFIGURASI WIFI & SERVER
// -----------------------------------------------------------------------------
// Ganti dengan Nama dan Password Wi-Fi yang sama dengan laptop/PC Anda:
const char* WIFI_SSID     = "Poco X3 Oguri";
const char* WIFI_PASSWORD = "qwertyuiop";

// Endpoint server backend Go:
const char* SERVER_URL    = "http://10.229.23.195:8080/api/v1/devices/ping";

// Device Token (sesuai DEVICE_TOKEN di file .env backend):
const char* DEVICE_TOKEN  = "HPYTy93zrOCwWjE2QakbxX0vi7gBnVuels8dFNZG";

// ID Perangkat yang terdaftar di database (muncul di dashboard sebagai ESP32):
const char* DEVICE_ID     = "ESP32-GED-A1";

// Interval pengiriman heartbeat ping (milidetik).
// Dashboard menganggap perangkat offline jika tidak ada ping selama 2 menit.
// Disarankan mengirim ping setiap 15-30 detik:
const unsigned long PING_INTERVAL_MS = 15000;

// LED Indikator bawaan ESP8266 (NodeMCU: D4/GPIO2 atau D0/GPIO16)
#ifndef LED_BUILTIN
  #define LED_BUILTIN 2
#endif

unsigned long lastPingTime = 0;

void blinkLED(int count, int delayMs) {
  for (int i = 0; i < count; i++) {
    digitalWrite(LED_BUILTIN, LOW);   // LED ON (active low)
    delay(delayMs);
    digitalWrite(LED_BUILTIN, HIGH);  // LED OFF
    delay(delayMs);
  }
}

void connectToWiFi() {
  Serial.println();
  Serial.print("[WiFi] Menghubungkan ke: ");
  Serial.println(WIFI_SSID);

  WiFi.mode(WIFI_STA);
  WiFi.begin(WIFI_SSID, WIFI_PASSWORD);

  int attempts = 0;
  while (WiFi.status() != WL_CONNECTED) {
    delay(500);
    Serial.print(".");
    attempts++;
    if (attempts > 40) { // Timeout 20 detik
      Serial.println();
      Serial.println("[WiFi] Gagal terhubung, mencoba ulang...");
      attempts = 0;
    }
  }

  Serial.println();
  Serial.println("[WiFi] Berhasil terhubung!");
  Serial.print("[WiFi] IP Address ESP8266: ");
  Serial.println(WiFi.localIP());
  Serial.print("[WiFi] Subnet: ");
  Serial.println(WiFi.subnetMask());
  Serial.print("[WiFi] Gateway: ");
  Serial.println(WiFi.gatewayIP());
  
  // Kedipan 3x tanda Wi-Fi tersambung
  blinkLED(3, 100);
}

void sendHeartbeatPing() {
  if (WiFi.status() != WL_CONNECTED) {
    Serial.println("[Ping] Wi-Fi terputus, menyambungkan ulang...");
    connectToWiFi();
    return;
  }

  WiFiClient client;
  HTTPClient http;

  Serial.println("--------------------------------------------------");
  Serial.print("[Ping] Mengirim heartbeat ke: ");
  Serial.println(SERVER_URL);

  if (http.begin(client, SERVER_URL)) {
    // Header request
    http.addHeader("Content-Type", "application/x-www-form-urlencoded");
    http.addHeader("Authorization", String("Bearer ") + DEVICE_TOKEN);
    http.addHeader("X-Device-Id", DEVICE_ID);

    // Body data
    String postData = String("device_id=") + DEVICE_ID;

    // Kirim request POST
    int httpResponseCode = http.POST(postData);

    if (httpResponseCode > 0) {
      Serial.print("[Ping] HTTP Status Code: ");
      Serial.println(httpResponseCode);

      String response = http.getString();
      Serial.print("[Ping] Response Server : ");
      Serial.println(response);

      if (httpResponseCode == 200 || httpResponseCode == 202) {
        Serial.println("[Sukses] >> Status di Dashboard web kini: 'Terhubung' (Online) <<");
        // Kedipkan LED 1x sebagai tanda ping diterima
        blinkLED(1, 150);
      }
    } else {
      Serial.print("[Error] Gagal terhubung ke server. Kode error: ");
      Serial.println(httpResponseCode);
      Serial.println("[Catatan] Pastikan server Go aktif dan port 8080 tidak diblokir Windows Firewall.");
    }

    http.end();
  } else {
    Serial.println("[Error] Tidak dapat membuat koneksi HTTP client.");
  }
}

void setup() {
  Serial.begin(115200);
  delay(1000);

  pinMode(LED_BUILTIN, OUTPUT);
  digitalWrite(LED_BUILTIN, HIGH); // Matikan LED di awal

  Serial.println("\n==================================================");
  Serial.println(" ESP8266 Heartbeat Client - Algorithmics Attendance");
  Serial.println(" Target Server: 10.229.23.195:8080");
  Serial.println(" Device ID    : ESP32-GED-A1 (Mapping ke Dashboard)");
  Serial.println("==================================================");

  connectToWiFi();

  // Kirim ping pertama saat baru menyala
  sendHeartbeatPing();
  lastPingTime = millis();
}

void loop() {
  unsigned long currentMillis = millis();

  // Kirim heartbeat secara berkala setiap PING_INTERVAL_MS
  if (currentMillis - lastPingTime >= PING_INTERVAL_MS) {
    lastPingTime = currentMillis;
    sendHeartbeatPing();
  }
}
