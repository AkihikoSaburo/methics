// Salin berkas ini menjadi "secrets.h" di folder yang sama, lalu isi nilainya.
// secrets.h di-gitignore: JANGAN commit password Wi-Fi atau token.
#pragma once

// Wi-Fi yang SAMA dengan ESP32 utama (2,4 GHz).
#define WIFI_SSID      "nama-wifi"
#define WIFI_PASSWORD  "password-wifi"

// Token rahasia antara ESP32 utama dan kamera (bebas, mis. 24+ karakter acak).
// HARUS sama dengan CAM_TOKEN di devices/esp32_scanner/secrets.h.
#define CAM_TOKEN      "ganti-dengan-token-kamera-acak"
