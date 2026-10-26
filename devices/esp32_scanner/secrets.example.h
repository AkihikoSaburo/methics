// Salin berkas ini menjadi "secrets.h" di folder yang sama, lalu isi nilainya.
// secrets.h di-gitignore: JANGAN commit Wi-Fi password atau DEVICE_TOKEN.
#pragma once

#define WIFI_SSID      "nama-wifi"
#define WIFI_PASSWORD  "password-wifi"

// Sama persis dengan DEVICE_TOKEN di .env backend (>= 32 karakter).
// Dipakai sebagai Bearer token DAN kunci HMAC tanda tangan scan.
#define DEVICE_TOKEN   "isi-dengan-DEVICE_TOKEN-dari-.env"
