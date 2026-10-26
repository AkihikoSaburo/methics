# Perangkat (ESP32 / ESP32-CAM / ESP8266)

> Status: **[SEBAGIAN]**. Firmware perangkat absensi sudah ada dan lolos
> kompilasi (core ESP32 3.0.7) serta uji format request terhadap backend,
> tetapi **belum diuji di perangkat keras**. Terakhir diperbarui 26 Oktober 2026.

## 1. Ringkasan

| Folder | Board | Peran |
| --- | --- | --- |
| `devices/esp32_scanner/` | ESP32 Dev Module | **perangkat absensi**: ultrasonik → foto → `POST /api/v1/scans` → hasil di layar + buzzer; heartbeat |
| `devices/esp32cam_uart/` | AI Thinker ESP32-CAM | **kamera** untuk `esp32_scanner`; dikendalikan lewat UART, tanpa Wi-Fi |
| `devices/algorithmics_engine_ultrasonic_buzzer/` | ESP32 | sketch uji layar + ultrasonik + buzzer (tanpa jaringan); dasar `esp32_scanner` |
| `devices/esp32cam_stream/` | ESP32-CAM | sketch uji kamera lewat web (stream, `/capture`) untuk mengatur arah/fokus; **jangan dipakai di produksi** |
| `devices/esp8266_ping/` | ESP8266 | uji koneksi heartbeat (sebelum ESP32 tersedia) |

| Fungsi | Endpoint | Auth |
| --- | --- | --- |
| Heartbeat / status online | `GET` atau `POST /api/v1/devices/ping` | bearer `DEVICE_TOKEN` **hanya di production** |
| Cek kesehatan backend | `GET /api/v1/health[?device_id=…]` | tidak ada |
| Kirim scan wajah | `POST /api/v1/scans` | bearer `DEVICE_TOKEN` + HMAC-SHA256 + timestamp |

Ketiganya dipasang di router luar (`cmd/server/main.go`), sehingga **tidak**
melewati session maupun CSRF browser. Dashboard menampilkan perangkat
"Terhubung" bila ada perangkat dengan `last_seen` kurang dari **2 menit**.

## 2. Perangkat absensi ESP32 (`esp32_scanner` + `esp32cam_uart`)

```text
HC-SR04 ──► ESP32 utama ──UART 460800──► ESP32-CAM
TFT, buzzer ◄──┘   │  ◄──── JPEG + CRC32 ────┘
                   └── Wi-Fi ──► Go  POST /api/v1/scans (Bearer + HMAC)
                                     POST /api/v1/devices/ping (tiap 15 s)
```

### 2.1 Wiring

| Dari | Ke | Catatan |
| --- | --- | --- |
| ESP32-CAM **GPIO15** (TX1) | ESP32 **GPIO16** (RX2) | data foto |
| ESP32-CAM **GPIO14** (RX1) | ESP32 **GPIO17** (TX2) | perintah |
| ESP32-CAM **GND** | ESP32 **GND** | **wajib** disatukan |
| ESP32-CAM **5V** | catu 5 V ≥ 2 A | jangan dari pin 3V3 ESP32 utama; kamera + Wi-Fi butuh arus puncak tinggi |
| HC-SR04 TRIG / ECHO | GPIO26 / GPIO27 | ECHO lewat pembagi tegangan 1k + 2k (5 V → 3,3 V) |
| Buzzer (+) | GPIO22 | modul *low trigger*: ubah `BUZZER_ON_LEVEL` ke `LOW` |
| TFT ILI9341 | sesuai `User_Setup.h` TFT_eSPI | backlight `TFT_BL` (default GPIO21) |

- GPIO14/15 pada ESP32-CAM adalah pin kartu SD — **jangan pasang kartu SD**.
- GPIO16/17 harus tidak dipakai di `User_Setup.h` TFT_eSPI. Pada modul
  **ESP32-WROVER**, GPIO16/17 dipakai PSRAM; ganti `CAM_RX_PIN`/`CAM_TX_PIN`.
- UART0 ESP32-CAM (GPIO1/3) tetap untuk upload & log, jadi kabel ke ESP32 utama
  tidak perlu dicabut saat flashing.

### 2.2 Perilaku

1. Saat start: Wi-Fi → sinkron jam NTP (UTC) → `PING` ke kamera → heartbeat
   pertama. Layar menampilkan "Siap" atau "Periksa" (Wi-Fi/jam/kamera mana yang gagal).
2. Objek ≤ `DETECT_CM` (20 cm) terdeteksi 2× berturut-turut → layar menyala,
   "Memindai…", bip pendek, jeda 300 ms, lalu minta foto ke kamera.
3. Foto (JPEG ≤ 60 KB, VGA 640×480 bila ada PSRAM) dikirim ke
   `POST /api/v1/scans` dengan tanda tangan HMAC (§6). `liveness_passed`
   dikirim **`false`** (`LIVENESS_PASSED`): alat belum punya anti-spoof, jadi
   wajah yang cocok dicatat **Perlu ditinjau**, bukan Hadir.
4. Hasil di layar + pola buzzer:

   | Respons | Layar | Buzzer |
   | --- | --- | --- |
   | `202` status `Hadir` | **HADIR** (hijau) + nama + NIM | 1 bip panjang |
   | `202` status `Perlu ditinjau` | **DITINJAU** (oranye) + nama + alasan | 2 bip |
   | `202` status `Ditolak` | **DITOLAK** (merah) + alasan | 3 bip pendek |
   | error (HTTP 4xx/5xx, kamera, Wi-Fi, jam) | **GAGAL** + kode + pesan | 3 bip pendek |

5. Scan berikutnya baru terjadi setelah objek menjauh (~0,5 s) **dan** lewat
   `SCAN_COOLDOWN_MS` (8 s), agar satu orang tidak terpindai berulang.
6. Heartbeat `POST /api/v1/devices/ping` tiap 15 s menggantikan ESP8266.
7. Respons `401 invalid_signature` / `400 stale_capture` memicu sinkron ulang
   jam pada scan berikutnya.

### 2.3 Konfigurasi

`devices/esp32_scanner/secrets.h` (salin dari `secrets.example.h`; di-gitignore):

| Konstanta | Isi |
| --- | --- |
| `WIFI_SSID`, `WIFI_PASSWORD` | Wi-Fi 2,4 GHz yang menjangkau server |
| `DEVICE_TOKEN` | sama persis dengan `DEVICE_TOKEN` di `.env` backend |

Bagian atas `esp32_scanner.ino`:

| Konstanta | Default | Keterangan |
| --- | --- | --- |
| `SERVER_HOST`, `SERVER_PORT` | `10.229.23.195`, `8080` | IP komputer yang menjalankan Go |
| `DEVICE_ID` | `ESP32-GED-A1` | harus ada di koleksi `devices` (seed) |
| `CLASS_ID` | `IK24-A` | kelas di ruangan itu, **huruf besar** |
| `LIVENESS_PASSED` | `false` | lihat §2.2 butir 3 |
| `DETECT_CM`, `SCAN_COOLDOWN_MS`, `RESULT_SHOW_MS` | 20, 8000, 6000 | jarak deteksi & jeda |
| `CAM_BAUD`, `MAX_IMAGE_BYTES` | 460800, 60000 | **samakan** dengan `LINK_BAUD`, `MAX_JPEG_BYTES` di `esp32cam_uart` |

Di `esp32cam_uart.ino`: `FRAME_SIZE` (VGA), `JPEG_QUALITY` (12), `USE_FLASH` (0).

`CLASS_ID` masih tetap per perangkat; jadwal kuliah per ruangan belum ada di
backend.

### 2.4 Build & upload (Arduino IDE)

1. Board manager URL `https://espressif.github.io/arduino-esp32/package_esp32_index.json`,
   pasang paket **esp32** (diuji kompilasi dengan 3.0.7).
2. Library **TFT_eSPI** (Bodmer) dengan `User_Setup.h` yang sama seperti sketch
   `algorithmics_engine_ultrasonic_buzzer`, dan `#define LOAD_GFXFF` aktif.
3. **Kamera dulu** (upload terpisah dari ESP32 utama): buka
   `devices/esp32cam_uart`, board *AI Thinker ESP32-CAM*.
   - Dengan **ESP32-CAM-MB** (papan uploader USB): pasang kamera di MB,
     pilih port COM MB, upload. MB mengatur mode boot sendiri; bila muncul
     `Failed to connect`, tahan tombol **IO0** di MB, tekan **RST**, lepas IO0
     saat tulisan `Connecting...`.
   - Dengan adaptor USB-serial biasa: hubungkan GPIO0 ke GND saat upload,
     lepas, tekan RST.
   - Serial Monitor 115200 harus menampilkan `[CAM] siap, PSRAM=1`.
   - Setelah itu **lepas kamera dari MB**: pin GPIO14/15 tertutup oleh MB,
     jadi kabel ke ESP32 utama (§2.1) dipasang langsung ke pin kamera, dengan
     catu 5 V terpisah. MB baru dipakai lagi saat upload ulang (lepas kabel
     UART dulu).
4. **ESP32 utama**: lewat kabel USB-nya sendiri, buka `devices/esp32_scanner`,
   buat `secrets.h`, board *ESP32 Dev Module*, upload, Serial Monitor 115200.

### 2.5 Uji bertahap

Ketik di Serial Monitor ESP32 utama (akhiri dengan newline):

| Perintah | Menguji | Hasil yang diharapkan |
| --- | --- | --- |
| `status` | Wi-Fi, jam, kamera, memori | `WiFi=OK … jam=… (sinkron) kamera=OK` |
| `cam` | kabel UART | `[Cam] PONG` |
| `ping` | server & token | `[Ping] HTTP 200`; dashboard menjadi "Terhubung" |
| `scan` | alur lengkap tanpa sensor | `[Scan] foto … byte` lalu `[Scan] HTTP 202 {…}` |

`DEVICE_ID` harus ada di koleksi `devices` database yang dipakai server
(seed membuat `ESP32-GED-A1`/`B1` di setiap start), dan `CLASS_ID` harus
kelas yang ada; kalau tidak, scan ditolak `403 unknown_device` /
`400 unknown_class`.

Urutan disarankan: `cam` → `ping` → enroll wajahmu di halaman Mahasiswa →
`scan` → cek halaman Log (status, skor, foto bila Garage aktif) → uji dengan
sensor ultrasonik.

## 3. Protokol UART kamera

UART 8N1, 460800 baud, satu permintaan per waktu:

| Arah | Pesan |
| --- | --- |
| ESP32 → kamera | `PING\n` → kamera `PONG psram=<0\|1>\n` |
| ESP32 → kamera | `CAP\n` → kamera `IMG <panjang> <crc32hex>\n` + `<panjang>` byte JPEG |
| kamera → ESP32 | `ERR <alasan>\n` (`camera_init`, `capture`, `too_big <n>`, `unknown_command`) |
| kamera → ESP32 | `READY\n` saat start (diabaikan ESP32 utama) |

CRC-32 = CRC IEEE (sama dengan `zlib.crc32`). ESP32 utama menolak foto bila
panjang tidak lengkap dalam 6 detik atau CRC tidak cocok ("Foto rusak (CRC)").
Bila kabel panjang/berisik dan CRC sering gagal, turunkan baud di **kedua**
sketch (mis. 230400). Foto lebih besar dari 60 KB diulang dengan kualitas JPEG
20; bila masih terlalu besar, kamera membalas `ERR too_big`.

## 4. Firmware heartbeat ESP8266 (uji koneksi)

Berkas: `devices/esp8266_ping/esp8266_ping.ino`. Mengirim `POST` ke
`SERVER_URL` setiap `PING_INTERVAL_MS` (15 s) dengan header
`Authorization: Bearer <DEVICE_TOKEN>`, `X-Device-Id`, dan body
`device_id=<DEVICE_ID>`. Board: paket **esp8266**
(`http://arduino.esp8266.com/stable/package_esp8266com_index.json`), mis.
*NodeMCU 1.0*. Setelah `esp32_scanner` berjalan, ESP8266 tidak diperlukan lagi
(keduanya memakai `DEVICE_ID` yang sama).

### ⚠️ Jangan commit kredensial

Sketch ESP8266 dan `esp32cam_stream` menulis SSID/password Wi-Fi (dan ESP8266
juga `DEVICE_TOKEN`) langsung di kode. Versi lama sketch ESP8266 **sudah
ter-commit** (commit `9af21ce`), sehingga nilainya ada di riwayat Git:

1. **Ganti `DEVICE_TOKEN`** di `.env` dan perangkat, serta password Wi-Fi.
2. Sebelum meng-commit `esp32cam_stream` atau `esp8266_ping`, kosongkan nilai
   rahasianya atau pindahkan ke `secrets.h` seperti `esp32_scanner`
   (`secrets.h` sudah di-gitignore).

## 5. Endpoint heartbeat

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

## 6. Protokol scan HTTP

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

### Implementasi di firmware

`postScan()` di `esp32_scanner.ino` mengikuti kontrak ini: SHA-256 dan
HMAC-SHA256 lewat mbedTLS bawaan core ESP32 (`mbedtls_md`, `mbedtls_md_hmac`),
`X-Device-Timestamp` = `captured_at` = waktu NTP saat foto diterima, dan body
multipart ditulis bertahap ke `WiFiClient` (foto tidak disalin dua kali di
RAM). Format ini diuji terhadap `readImagePart` dan `validScanSignature`
backend. Koneksi masih HTTP biasa; untuk server di belakang TLS ganti ke
`WiFiClientSecure`.

## 7. Catatan keamanan

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
4. Firmware memakai HTTP biasa. Token terkirim tanpa enkripsi; di produksi
   gunakan TLS atau jaringan perangkat yang terisolasi.
5. `esp32cam_stream` membuka stream dan `/capture` **tanpa autentikasi** ke
   seluruh jaringan; pakai hanya untuk menyetel kamera. `esp32cam_uart` tidak
   memakai Wi-Fi sama sekali.

## 8. Pemecahan masalah

| Gejala | Kemungkinan penyebab |
| --- | --- |
| Layar "Periksa", kamera GAGAL / `[Cam] tidak merespons` | TX/RX tertukar, GND tidak disatukan, baud berbeda, kamera kurang daya (brownout), atau kartu SD terpasang |
| "Foto rusak (CRC)" / "Transfer foto terputus" | kabel UART terlalu panjang/berisik → turunkan baud di kedua sketch |
| `[CAM] init gagal 0x105` (log kamera) | kabel pita kamera longgar atau board bukan AI Thinker |
| "Jam belum sinkron" | NTP diblokir jaringan kampus; ganti `NTP_SERVER_1` ke server NTP lokal |
| "Server tidak terjangkau" (ESP32) | `SERVER_HOST` salah, Go tidak jalan, atau Windows Firewall memblokir port 8080 |
| Layar tidak menampilkan apa-apa / huruf aneh | `User_Setup.h` TFT_eSPI berbeda atau `LOAD_GFXFF` tidak aktif |
| Wajah cocok tetapi selalu "Perlu ditinjau" | memang begitu selama `LIVENESS_PASSED=false`, atau mahasiswa bukan anggota `CLASS_ID` |
| Serial: `Gagal terhubung ke server. Kode error: -1` (ESP8266) | IP/port salah, server tidak jalan, atau Windows Firewall memblokir port 8080 |
| HTTP `401 invalid_token` | `APP_ENV=production` dan token perangkat ≠ `DEVICE_TOKEN` |
| Dashboard tetap "offline" | interval ping > 2 menit, atau jam server bergeser |
| Scan `401 invalid_signature` | jam perangkat tidak sinkron, `class_id`/`captured_at` berbeda dari yang ditandatangani, atau hash dihitung dari byte berbeda |
| Scan `400 stale_capture` | `captured_at` lebih dari 5 menit dari jam server |
| Scan `403 unknown_device` | `device_id` belum ada di koleksi `devices` |
| Scan `502 face_service_error` | face service mati / `FACE_SERVICE_URL` salah |
| Kolom Foto di Log berisi `-` | penyimpanan foto nonaktif (`S3_*` kosong), Garage tidak terjangkau (lihat log `image storage: …`), scan dibuat sebelum `S3_*` diisi, atau foto sudah lewat masa retensi |
