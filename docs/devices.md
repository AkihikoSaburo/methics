# Perangkat (ESP8266 / ESP32)

> Status: **[SEBAGIAN]**. Firmware yang ada di repo hanya **heartbeat ping**
> ESP8266 (`devices/esp8266/esp8266_ping/`). Firmware kamera yang mengirim
> scan wajah (ESP32-CAM) **belum ada**; kontraknya sudah diterapkan di backend
> dan dijelaskan di §4.

## 1. Ringkasan

| Fungsi | Endpoint | Auth | Firmware di repo |
| --- | --- | --- | --- |
| Heartbeat / status online | `GET` atau `POST /api/v1/devices/ping` | bearer `DEVICE_TOKEN` **hanya di production** | ya (ESP8266) |
| Cek kesehatan backend | `GET /api/v1/health[?device_id=…]` | tidak ada | tidak |
| Kirim scan wajah | `POST /api/v1/scans` | bearer `DEVICE_TOKEN` + HMAC-SHA256 + timestamp | **belum** |

Ketiganya dipasang di router luar (`cmd/server/main.go`), sehingga **tidak**
melewati session maupun CSRF browser.

Dashboard menampilkan perangkat "Terhubung" bila ada perangkat dengan
`last_seen` kurang dari **2 menit** yang lalu. Nilai `last_seen` diperbarui oleh
ketiga endpoint di atas.

## 2. Firmware heartbeat ESP8266

Berkas: `devices/esp8266/esp8266_ping/esp8266_ping.ino`

Perilaku:

1. Tersambung ke Wi-Fi (mode station; mencoba ulang tiap 20 detik bila gagal).
2. Mengirim `POST` ke `SERVER_URL` setiap `PING_INTERVAL_MS` (default 15 detik)
   dengan header `Authorization: Bearer <DEVICE_TOKEN>`, `X-Device-Id`, dan body
   `device_id=<DEVICE_ID>` (form-urlencoded).
3. LED bawaan berkedip 3× saat Wi-Fi tersambung dan 1× setiap ping diterima
   (HTTP 200/202). Log diagnostik ada di Serial Monitor **115200 baud**.

### Konfigurasi

Ubah konstanta di bagian atas sketch:

| Konstanta | Isi |
| --- | --- |
| `WIFI_SSID`, `WIFI_PASSWORD` | jaringan Wi-Fi yang sama dengan server |
| `SERVER_URL` | `http://<IP-server>:8080/api/v1/devices/ping` |
| `DEVICE_TOKEN` | sama dengan `DEVICE_TOKEN` backend |
| `DEVICE_ID` | ID di koleksi `devices` (default `ESP32-GED-A1`) |
| `PING_INTERVAL_MS` | interval ping; harus < 120000 agar tetap "online" |

### Build & unggah (Arduino IDE)

1. Tambahkan board manager URL
   `http://arduino.esp8266.com/stable/package_esp8266com_index.json`, lalu
   pasang paket **esp8266**.
2. Pilih board (mis. *NodeMCU 1.0 (ESP-12E Module)* atau *LOLIN(WEMOS) D1 mini*)
   dan port USB.
3. Upload, lalu buka Serial Monitor 115200 baud.

Pustaka yang dipakai (`ESP8266WiFi`, `ESP8266HTTPClient`, `WiFiClient`) sudah
termasuk paket board.

### ⚠️ Jangan commit kredensial

Sketch saat ini menulis SSID, password Wi-Fi, dan `DEVICE_TOKEN` langsung di
kode, dan sketch itu **sudah ter-commit** (commit `9af21ce`), sehingga nilainya
tersimpan di riwayat Git. Langkah perbaikan:

1. Pindahkan nilai rahasia ke `secrets.h` di folder sketch:

   ```cpp
   // devices/esp8266/esp8266_ping/secrets.h  (JANGAN di-commit)
   #pragma once
   #define WIFI_SSID_VALUE     "nama-wifi"
   #define WIFI_PASSWORD_VALUE "password-wifi"
   #define DEVICE_TOKEN_VALUE  "token-acak-minimal-32-karakter"
   ```

   lalu di sketch: `#include "secrets.h"` dan
   `const char* WIFI_SSID = WIFI_SSID_VALUE;` (dst.).
2. Tambahkan `devices/**/secrets.h` ke `.gitignore` dan sediakan
   `secrets.example.h` berisi placeholder.
3. **Ganti `DEVICE_TOKEN`** di `.env` dan perangkat, serta password Wi-Fi,
   karena nilai lama sudah ada di riwayat Git. Menghapus berkas dari commit
   berikutnya tidak menghapusnya dari riwayat.

## 3. Endpoint heartbeat

`GET|POST /api/v1/devices/ping`

ID perangkat dibaca berurutan dari: field form `device_id` → query
`?device_id=` → header `X-Device-Id` → **default `ESP32-GED-A1`**.

| Mode | Token |
| --- | --- |
| `APP_ENV=production` | wajib `Authorization: Bearer <DEVICE_TOKEN>`; salah → `401 invalid_token` |
| selain itu | **tidak diperiksa** (memudahkan uji coba di LAN) |

Efek: upsert `devices` — `last_seen` di-set ke sekarang (RFC3339 UTC); bila ID
belum ada, dokumen baru dibuat dengan nama `Perangkat <id>`.

Respons `200`:

```json
{
  "status": "ok",
  "device_id": "ESP32-GED-A1",
  "last_seen": "2026-10-21T00:15:00Z",
  "message": "Perangkat berhasil terhubung dan status aktif."
}
```

Uji manual:

```bash
curl -X POST http://localhost:8080/api/v1/devices/ping \
  -H "Authorization: Bearer $DEVICE_TOKEN" \
  -d device_id=ESP32-GED-A1
```

## 4. Protokol scan (untuk firmware ESP32-CAM)

`POST /api/v1/scans` — `multipart/form-data`, batas total body **8 MiB**.

| Bagian | Nama | Isi |
| --- | --- | --- |
| header | `Authorization` | `Bearer <DEVICE_TOKEN>` |
| header | `X-Device-Timestamp` | waktu kirim RFC3339, selisih ≤ 2 menit dari server |
| header | `X-Device-Signature` | hex HMAC-SHA256 (lihat di bawah), huruf kecil |
| field | `device_id` | harus ada di koleksi `devices` |
| field | `class_id` | harus ada di `classes` (dinormalisasi ke huruf besar) |
| field | `captured_at` | waktu capture RFC3339, selisih ≤ 5 menit dari server |
| field | `liveness_passed` | `true` atau `false` |
| file | `image` | JPEG/PNG, tidak kosong, ≤ 8 MiB |

Pesan yang ditandatangani = enam baris digabung `\n` (tanpa newline penutup):

```text
<X-Device-Timestamp>
<device_id>
<class_id>            ← huruf besar (server menandatangani versi yang sudah di-uppercase)
<captured_at>         ← persis seperti dikirim
<liveness_passed>     ← "true" / "false" huruf kecil
<hex SHA-256 dari byte image>
```

Kunci HMAC = `DEVICE_TOKEN`. Server menghitung ulang pesan dari nilai yang
sudah dinormalisasi (spasi di tepi dibuang, `class_id` di-uppercase,
`liveness_passed` di-lowercase). Kirim nilai yang sudah dalam bentuk itu agar
tanda tangan cocok.

Karena timestamp dicek ±2 menit, perangkat **wajib** menyinkronkan jam lewat
NTP (`configTime(...)` di ESP32) sebelum mengirim scan.

### Klien uji (Python)

Berguna untuk menguji backend tanpa perangkat keras:

```python
# scan_client.py — python scan_client.py foto.jpg
import hashlib, hmac, os, sys
from datetime import datetime, timezone
import requests  # pip install requests

BASE = os.environ.get("BASE_URL", "http://localhost:8080")
TOKEN = os.environ["DEVICE_TOKEN"]
device_id, class_id, liveness = "ESP32-GED-A1", "IK24-A", "true"

image = open(sys.argv[1], "rb").read()
now = datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")
message = "\n".join([now, device_id, class_id, now, liveness, hashlib.sha256(image).hexdigest()])
signature = hmac.new(TOKEN.encode(), message.encode(), hashlib.sha256).hexdigest()

response = requests.post(
    f"{BASE}/api/v1/scans",
    headers={
        "Authorization": f"Bearer {TOKEN}",
        "X-Device-Timestamp": now,
        "X-Device-Signature": signature,
    },
    data={"device_id": device_id, "class_id": class_id, "captured_at": now, "liveness_passed": liveness},
    files={"image": ("scan.jpg", image, "image/jpeg")},
    timeout=15,
)
print(response.status_code, response.text)
```

Scan baru berhasil dicatat bila face service aktif; bila tidak, backend
membalas `502 face_service_error` dan **tidak** menyimpan event. Bila
penyimpanan foto (`S3_*`) aktif, capture juga disimpan ke Garage dan bisa
dibuka dari kolom **Foto** di halaman Log.

### Catatan implementasi ESP32

- HMAC-SHA256 dan SHA-256 tersedia lewat mbedTLS bawaan core ESP32
  (`mbedtls/md.h`: `mbedtls_md_hmac_starts/update/finish` dengan
  `MBEDTLS_MD_SHA256`).
- Hitung SHA-256 gambar dari buffer frame kamera (`camera_fb_t->buf`,
  `->len`) yang sama dengan yang dikirim.
- Gunakan HTTPS (`WiFiClientSecure`) bila server berada di belakang TLS.
- `liveness_passed` hanyalah klaim perangkat. Backend mencatat `Hadir` hanya
  bila nilainya `true`, tetapi tidak dapat memverifikasinya; lihat
  `HANDOFF.md` §Batasan.

## 5. Catatan keamanan

1. **Satu `DEVICE_TOKEN` untuk semua perangkat.** Bocor satu perangkat berarti
   semua perangkat bisa ditiru. Rencana: secret per perangkat + rotasi.
2. **Ping tanpa token di mode non-production** dan **health dengan
   `?device_id=` tanpa token di semua mode** dapat menandai perangkat online
   dan **membuat dokumen `devices` baru** untuk ID apa pun. Perangkat yang
   terbentuk dengan cara ini lolos pengecekan "perangkat terdaftar" pada
   `/api/v1/scans` (scan tetap butuh token + HMAC yang valid). Sebelum
   produksi, pertimbangkan: ping dan health tidak melakukan upsert, hanya
   memperbarui perangkat yang sudah terdaftar.
3. **Belum ada nonce anti-replay.** Request scan yang sama bisa diputar ulang
   dalam jendela 2 menit.
4. Sketch ESP8266 memakai HTTP biasa. Token terkirim tanpa enkripsi; di
   produksi gunakan TLS atau jaringan perangkat yang terisolasi.

## 6. Pemecahan masalah

| Gejala | Kemungkinan penyebab |
| --- | --- |
| Serial: `Gagal terhubung ke server. Kode error: -1` | IP/port salah, server tidak jalan, atau Windows Firewall memblokir port 8080 |
| HTTP `401 invalid_token` | `APP_ENV=production` dan token perangkat ≠ `DEVICE_TOKEN` |
| Dashboard tetap "offline" | interval ping > 2 menit, atau jam server bergeser |
| Scan `401 invalid_signature` | jam perangkat tidak sinkron, `class_id`/`captured_at` berbeda dari yang ditandatangani, atau hash dihitung dari byte berbeda |
| Scan `400 stale_capture` | `captured_at` lebih dari 5 menit dari jam server |
| Scan `403 unknown_device` | `device_id` belum ada di koleksi `devices` |
| Scan `502 face_service_error` | face service mati / `FACE_SERVICE_URL` salah |
| Kolom Foto di Log berisi `-` | penyimpanan foto nonaktif (`S3_*` kosong), Garage tidak terjangkau (lihat log `image storage: …`), scan dibuat sebelum `S3_*` diisi, atau foto sudah lewat masa retensi |
