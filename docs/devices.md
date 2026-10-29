# Perangkat (ESP32 / ESP32-CAM / ESP8266)

> Status: **[SEBAGIAN]**. Firmware perangkat absensi sudah ada dan lolos
> kompilasi (core ESP32 3.0.7) serta uji format request terhadap backend,
> tetapi **belum diuji di perangkat keras**. Terakhir diperbarui 28 Oktober 2026
> (scan berbasis stream: kamera menyala saat ada orang, ±2 detik multi-frame).

## 1. Ringkasan

| Folder | Board | Peran |
| --- | --- | --- |
| `devices/esp32_scanner/` | ESP32 Dev Module | **perangkat absensi**: ultrasonik → kamera ON → ±2 detik frame → `POST /api/v1/scans` (multi-frame) → hasil di layar + buzzer; heartbeat |
| `devices/esp32cam_uart/` | AI Thinker ESP32-CAM | **kamera** untuk `esp32_scanner`; mati saat idle, menyala + stream atas perintah UART, tanpa Wi-Fi |
| `devices/esp32cam_wifi/` | AI Thinker ESP32-CAM | **kamera mode uji Wi-Fi**: pengganti kabel UART saat kamera belum bisa dikabel/dicatu bersama ESP32 utama (§2.6) |
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
HC-SR04 ──► ESP32 utama ──UART 460800 "ON" / "CAP"×4 / "OFF"──► ESP32-CAM (stream QVGA)
TFT, buzzer ◄──┘   │  ◄──────── JPEG + CRC32 per frame ────────┘
                   └── Wi-Fi ──► Go  POST /api/v1/scans (1–6 frame, Bearer + HMAC)
                                     │   └─► Python /embed per frame → cosine vs MongoDB
                                     POST /api/v1/devices/ping (tiap 15 s)
```

Rangkaian **tidak berubah** dari versi sebelumnya; yang berubah hanya
firmware kedua board dan logika scan di Go.

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
   pertama. Layar menampilkan "Siap" atau "Periksa" (Wi-Fi/jam/kamera mana
   yang gagal). Kamera dalam keadaan **mati** (power-down) sampai ada orang.
2. Objek ≤ `DETECT_CM` (20 cm) terdeteksi 2× berturut-turut → layar menyala,
   "Kamera menyala", bip pendek, ESP32 mengirim `ON` ke kamera. Kamera
   menyala (±0,5–1 detik termasuk pemanasan exposure) lalu terus menangkap
   frame ke buffer PSRAM.
3. **Jendela scan ±2 detik** (`SCAN_WINDOW_MS`): layar "Memindai..." dengan
   garis progres; ESP32 mengambil hingga `SCAN_MAX_FRAMES` (4) frame JPEG
   QVGA (±8–15 KB) yang tersebar merata, lalu mengirim `OFF` ke kamera.
4. Semua frame dikirim **dalam satu request** ke `POST /api/v1/scans`
   (field `image` berulang) dengan tanda tangan HMAC (§6). Go mencocokkan
   setiap frame dengan semua wajah terdaftar (semua pose enrollment):
   - wajah yang sama cocok di **≥ 2 frame** (`SCAN_MIN_MATCH_FRAMES` di
     server) → **Hadir**, tercatat di kelas mahasiswa itu (`CLASS_ID` kosong =
     kelas otomatis) dan langsung tampil di dashboard;
   - ada wajah tetapi tidak cocok dengan siapa pun → **Ditolak**, layar
     **"Wajah tidak Terdaftar"**;
   - tidak ada wajah di semua frame → **Ditolak**, "Wajah tidak terdeteksi";
   - cocok di 1 frame saja, atau mahasiswa bukan anggota `CLASS_ID` tetap
     perangkat → **Perlu ditinjau**.
   `liveness_passed` tetap dikirim `false`; kecocokan beberapa frame
   **bukan** anti-spoof (foto cetak di depan kamera bisa lolos), lihat §7.
5. Hasil di layar + pola buzzer (dari field `result` respons):

   | `result` | Layar | Buzzer |
   | --- | --- | --- |
   | `hadir` | **HADIR** (hijau) + nama + NIM + kelas | 1 bip panjang |
   | `tidak_terdaftar` | **Wajah tidak Terdaftar** (merah) | 3 bip pendek |
   | `wajah_tidak_terdeteksi` | **Wajah tidak terdeteksi** (oranye) + "Hadapkan wajah ke kamera" | 2 bip |
   | `perlu_ditinjau` | **DITINJAU** (oranye) + nama + alasan | 2 bip |
   | error (HTTP 4xx/5xx, kamera, Wi-Fi, jam) | **GAGAL** + kode + pesan | 3 bip pendek |

6. Scan berikutnya baru terjadi setelah objek menjauh (~0,5 s) **dan** lewat
   `SCAN_COOLDOWN_MS` (6 s), agar satu orang tidak terpindai berulang.
7. Heartbeat `POST /api/v1/devices/ping` tiap 15 s.
8. Respons `401 invalid_signature` / `400 stale_capture` memicu sinkron ulang
   jam pada scan berikutnya.

Perkiraan waktu dari orang berdiri di depan alat sampai hasil tampil:
kamera ON ±1 s + jendela scan ±2 s + kirim & cocokkan ±0,5–1,5 s (Go
berhenti lebih awal begitu 2 frame cocok).

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
| `CLASS_ID` | `""` (kosong) | kosong = **kelas otomatis** (kehadiran dicatat di kelas mahasiswa yang cocok); isi kode kelas huruf besar (mis. `IK24-A`) bila alat khusus satu kelas |
| `LIVENESS_PASSED` | `false` | lihat §2.2 butir 4 |
| `SCAN_WINDOW_MS`, `SCAN_MAX_FRAMES` | 2000, 4 | lama jendela scan dan jumlah frame (maks 6, batas server) |
| `DETECT_CM`, `SCAN_COOLDOWN_MS`, `RESULT_SHOW_MS` | 20, 6000, 6000 | jarak deteksi & jeda |
| `CAM_BAUD`, `MAX_IMAGE_BYTES` | 460800, 24000 | **samakan** dengan `LINK_BAUD`, `MAX_JPEG_BYTES` di `esp32cam_uart` |
| `CAM_ON_TIMEOUT_MS`, `CAM_TIMEOUT_MS` | 5000, 4000 | batas kamera menyala dan batas satu frame |

Di `esp32cam_uart.ino`: `FRAME_SIZE` (QVGA 320×240), `JPEG_QUALITY` (10),
`WARMUP_FRAMES` (4 frame awal dibuang agar exposure stabil), `IDLE_OFF_MS`
(15 s, kamera mati sendiri bila tidak ada perintah), `USE_FLASH` (0; 1 = LED
flash menyala selama kamera ON, berguna di ruangan gelap).

Sisi server (`.env` Go): `SCAN_MIN_MATCH_FRAMES` (default 2) dan
`FACE_SIM_THRESHOLD` (default 0.45).

Memori: 4 frame × ≤ 24 KB ≈ 100 KB RAM di ESP32 utama (WROOM tanpa PSRAM).
Bila `SCAN_MAX_FRAMES` atau `MAX_IMAGE_BYTES` dinaikkan, cek `maxblok` pada
perintah `status`; firmware berhenti mengambil frame bila heap tersisa
< `HEAP_RESERVE` (40 KB).

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
   - Serial Monitor 115200 harus menampilkan
     `[CAM] UART1 460800 baud, … kamera OK (idle: mati)`, dan `[CAM] ON dalam … ms (PSRAM=1)`
     setiap kali ada scan.
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
| `camon` / `camoff` | daya & sensor kamera | `[Cam] ON OK` / `[Cam] OFF` |
| `ping` | server & token | `[Ping] HTTP 200`; dashboard menjadi "Terhubung" |
| `scan` | alur lengkap tanpa sensor | `[Scan] kamera ON …`, `[Scan] frame 1..4: … byte`, lalu `[Scan] HTTP 202 … {"result":"hadir",…}` |

`DEVICE_ID` harus ada di koleksi `devices` database yang dipakai server
(seed membuat `ESP32-GED-A1`/`B1` di setiap start), dan bila `CLASS_ID`
diisi, kelasnya harus ada; kalau tidak, scan ditolak `403 unknown_device` /
`400 unknown_class`.

Urutan disarankan: `cam` → `camon`/`camoff` → `ping` → enroll wajahmu di
halaman Mahasiswa (6 pose) → `scan` → cek halaman Log (status, skor, foto
bila Garage aktif) → uji dengan sensor ultrasonik → uji dengan orang yang
belum di-enroll (harus "Wajah tidak Terdaftar"). Terminal Go menulis log
`scan ESP32-GED-A1: Hadir (hadir) frame=4 wajah=… cocok=… skor=…`.

### 2.6 Mode uji: kamera lewat Wi-Fi (tanpa kabel antar-board)

Dipakai bila ESP32-CAM belum bisa dicatu dan dikabel bersama ESP32 utama
(mis. belum ada kabel/catu 5 V terpisah). ESP32-CAM tetap terpasang di
**ESP32-CAM-MB** dengan USB-nya sendiri, lalu tersambung ke Wi-Fi yang sama
dengan ESP32 utama. **Tidak ada kabel apa pun** di antara kedua board.

```text
HC-SR04 ──► ESP32 utama ──Wi-Fi (LAN) POST /on, GET /capture ×4, POST /off──► ESP32-CAM (di MB, USB sendiri)
TFT, buzzer ◄──┘   └── Wi-Fi ──► Go  POST /api/v1/scans (sama seperti mode UART)
```

Alur scan, frame, dan hasil di layar **sama persis** dengan mode UART (§2.2);
yang berbeda hanya jalur ESP32 utama ↔ kamera.

**Langkah:**

1. **Kamera**: buka `devices/esp32cam_wifi`, salin `secrets.example.h` menjadi
   `secrets.h`, isi `WIFI_SSID`, `WIFI_PASSWORD` (Wi-Fi yang sama dengan ESP32
   utama, 2,4 GHz) dan `CAM_TOKEN` (teks acak bebas). Upload lewat MB
   (board *AI Thinker ESP32-CAM*). Biarkan kamera tetap di MB dan tetap
   tercolok USB (lebih stabil dengan charger HP 5V 2A daripada port laptop).
2. Serial Monitor kamera (115200) menampilkan
   `[WiFi] IP kamera: 192.168.x.x` dan `[mDNS] http://methics-cam.local`.
3. **ESP32 utama**: tambahkan ke `devices/esp32_scanner/secrets.h`
   ```c
   #define CAM_TOKEN "token-yang-sama-dengan-kamera"
   ```
   Di `esp32_scanner.ino` pastikan `#define CAM_LINK_WIFI 1`. Bila mDNS tidak
   jalan di jaringanmu (sering terjadi di hotspot HP / Wi-Fi kampus), isi
   `#define CAM_HOST "192.168.x.x"` dengan IP dari langkah 2. Upload.
4. Uji di Serial Monitor ESP32 utama: `cam` → `[Cam] PONG`, `camon` /
   `camoff`, lalu `scan`.

| Endpoint kamera | Fungsi |
| --- | --- |
| `GET /ping` | `PONG psram=<0\|1> cam=<on\|off>` |
| `POST /on` | kamera menyala + pemanasan exposure → `ON` |
| `GET /capture` | frame JPEG terbaru (`image/jpeg`, ≤ 24 KB); kamera menyala dulu bila mati |
| `POST /off` | kamera power-down → `OFF` |

Semua endpoint wajib header `X-Cam-Token: <CAM_TOKEN>` (salah → `401`,
layar "Token kamera salah"). Kamera tetap mati sendiri setelah 15 s tanpa
permintaan.

**Batasan mode uji:** jalur kamera memakai HTTP biasa di LAN (token terkirim
tanpa enkripsi) dan bergantung pada kualitas Wi-Fi; latensi per frame
±100–300 ms. Untuk pemasangan permanen kembali ke kabel UART
(`CAM_LINK_WIFI 0` + `esp32cam_uart`) dengan catu 5 V bersama dan GND
disatukan.

## 3. Protokol UART kamera

UART 8N1, 460800 baud, satu permintaan per waktu:

| Arah | Pesan |
| --- | --- |
| ESP32 → kamera | `PING\n` → kamera `PONG psram=<0\|1> cam=<on\|off>\n` |
| ESP32 → kamera | `ON\n` → kamera menyala, membuang `WARMUP_FRAMES` frame, lalu `ON\n` |
| ESP32 → kamera | `CAP\n` → kamera `IMG <panjang> <crc32hex>\n` + `<panjang>` byte JPEG (frame terbaru dari stream; bila kamera mati, menyala dulu) |
| ESP32 → kamera | `OFF\n` → kamera power-down, lalu `OFF\n` |
| kamera → ESP32 | `ERR <alasan>\n` (`camera_init`, `capture`, `too_big <n>`, `unknown_command`) |
| kamera → ESP32 | `READY\n` saat start (diabaikan ESP32 utama) |

Dengan PSRAM, driver kamera memakai 2 buffer dan `CAMERA_GRAB_LATEST`, jadi
selama ON sensor terus menangkap (stream) dan setiap `CAP` langsung
mendapat frame terbaru tanpa jeda. Kamera mati sendiri setelah
`IDLE_OFF_MS` (15 s) tanpa perintah sebagai pengaman.

CRC-32 = CRC IEEE (sama dengan `zlib.crc32`). ESP32 utama menolak frame bila
panjang tidak lengkap dalam `CAM_TIMEOUT_MS` (4 s) atau CRC tidak cocok
("Foto rusak (CRC)"); frame yang gagal dilewati dan scan memakai frame
lainnya. Bila kabel panjang/berisik dan CRC sering gagal, turunkan baud di
**kedua** sketch (mis. 230400). Frame lebih besar dari 24 KB diulang dengan
kualitas JPEG 18; bila masih terlalu besar, kamera membalas `ERR too_big`.

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

`POST /api/v1/scans` — `multipart/form-data`, batas total body **16 MiB**.

| Bagian | Nama | Isi |
| --- | --- | --- |
| header | `Authorization` | `Bearer <DEVICE_TOKEN>` |
| header | `X-Device-Timestamp` | waktu kirim RFC3339, selisih ≤ 2 menit dari server |
| header | `X-Device-Signature` | hex HMAC-SHA256 (lihat di bawah), huruf kecil |
| field | `device_id` | harus ada di koleksi `devices` |
| field | `class_id` | **opsional**: kosong = kelas otomatis (kelas mahasiswa yang cocok); bila diisi harus ada di `classes` (dinormalisasi ke huruf besar) |
| field | `captured_at` | waktu capture RFC3339, selisih ≤ 5 menit dari server |
| field | `liveness_passed` | `true` atau `false` |
| file | `image` (1–6 kali) | frame JPEG/PNG berurutan, masing-masing tidak kosong dan ≤ 8 MiB |

Pesan yang ditandatangani = enam baris digabung `\n` (tanpa newline penutup):

```text
<X-Device-Timestamp>
<device_id>
<class_id>            ← huruf besar; baris tetap ada walau kosong (kelas otomatis)
<captured_at>         ← persis seperti dikirim
<liveness_passed>     ← "true" / "false" huruf kecil
<hex SHA-256 dari semua frame digabung berurutan>   ← satu frame = SHA-256 gambar itu
```

Kunci HMAC = `DEVICE_TOKEN`. Server menghitung ulang pesan dari nilai yang
sudah dinormalisasi (spasi di tepi dibuang, `class_id` di-uppercase,
`liveness_passed` di-lowercase). Kirim nilai yang sudah dalam bentuk itu agar
tanda tangan cocok. Menghapus, menambah, atau menukar urutan frame membuat
tanda tangan tidak valid.

Respons `202`:

```json
{
  "status": "Hadir", "result": "hadir", "message": "Hadir",
  "student": { "id": "student-241011001", "nim": "241011001", "name": "Alya Putri" },
  "classId": "IK24-A", "faceVerified": true, "confidence": 0.8123, "reviewReason": "",
  "framesTotal": 4, "framesWithFace": 2, "framesMatched": 2
}
```

`result` ∈ `hadir`, `perlu_ditinjau`, `tidak_terdaftar`,
`wajah_tidak_terdeteksi`; `message` adalah teks pendek untuk layar
(`"Wajah tidak Terdaftar"` untuk `tidak_terdaftar`). `framesWithFace` bisa
lebih kecil dari `framesTotal` karena Go berhenti memproses begitu
`SCAN_MIN_MATCH_FRAMES` frame cocok.

Karena timestamp dicek ±2 menit, perangkat **wajib** menyinkronkan jam lewat
NTP (`configTime(...)` di ESP32) sebelum mengirim scan.

### Klien uji (Python)

Berguna untuk menguji backend tanpa perangkat keras:

```python
# scan_client.py — python scan_client.py frame1.jpg frame2.jpg [... maks 6]
import hashlib, hmac, os, sys
from datetime import datetime, timezone
import requests  # pip install requests

BASE = os.environ.get("BASE_URL", "http://localhost:8080")
TOKEN = os.environ["DEVICE_TOKEN"]
device_id, class_id, liveness = "ESP32-GED-A1", "", "false"   # class_id "" = kelas otomatis

frames = [open(path, "rb").read() for path in sys.argv[1:]]
now = datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")
frames_hash = hashlib.sha256(b"".join(frames)).hexdigest()
message = "\n".join([now, device_id, class_id, now, liveness, frames_hash])
signature = hmac.new(TOKEN.encode(), message.encode(), hashlib.sha256).hexdigest()

response = requests.post(
    f"{BASE}/api/v1/scans",
    headers={
        "Authorization": f"Bearer {TOKEN}",
        "X-Device-Timestamp": now,
        "X-Device-Signature": signature,
    },
    data={"device_id": device_id, "class_id": class_id, "captured_at": now, "liveness_passed": liveness},
    files=[("image", (f"frame-{i + 1}.jpg", data, "image/jpeg")) for i, data in enumerate(frames)],
    timeout=15,
)
print(response.status_code, response.text)
```

Kirim 2 foto wajahmu (yang sudah di-enroll) → `"result":"hadir"`; kirim foto
orang lain → `"result":"tidak_terdaftar"`. Scan baru berhasil dicatat bila
face service aktif; bila tidak, backend membalas `502 face_service_error` dan
**tidak** menyimpan event. Bila penyimpanan foto (`S3_*`) aktif, **satu**
frame bukti (frame dengan skor terbaik) disimpan ke Garage dan bisa dibuka
dari kolom **Foto** di halaman Log.

### Implementasi di firmware

`postScan()` di `esp32_scanner.ino` mengikuti kontrak ini: SHA-256 bertahap
atas semua frame dan HMAC-SHA256 lewat mbedTLS bawaan core ESP32
(`mbedtls_md_starts/update/finish`, `mbedtls_md_hmac`), `X-Device-Timestamp` =
`captured_at` = waktu NTP setelah frame terakhir diterima, dan body multipart
ditulis bertahap ke `WiFiClient` (frame tidak disalin dua kali di RAM).
Format ini (termasuk perhitungan `Content-Length`) diuji terhadap
`readScanFrames` dan `validScanSignature` backend. Koneksi masih HTTP biasa; untuk server di belakang TLS ganti ke
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
4. **"Hadir" dari beberapa frame bukan anti-spoof.** Konsensus ≥ 2 frame
   hanya memastikan wajah yang sama terlihat konsisten selama ±2 detik; foto
   cetak atau layar HP di depan kamera tetap bisa lolos. Untuk mencegah titip
   absen, tambahkan liveness (mis. kedipan/gerak kepala di Python atau sensor
   kedalaman) lalu kirim `liveness_passed=true` hanya bila lolos, dan naikkan
   `SCAN_MIN_MATCH_FRAMES` di atas jumlah frame agar konsensus saja tidak cukup.
5. Firmware memakai HTTP biasa. Token terkirim tanpa enkripsi; di produksi
   gunakan TLS atau jaringan perangkat yang terisolasi.
6. `esp32cam_stream` membuka stream dan `/capture` **tanpa autentikasi** ke
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
| Wajah terdaftar tetapi "Perlu ditinjau" | wajah cocok di < 2 frame (wajah bergerak/miring, cahaya kurang, jarak terlalu dekat), atau `CLASS_ID` diisi dan mahasiswa bukan anggota kelas itu |
| Wajah terdaftar tetapi "Wajah tidak Terdaftar" | enrollment dengan foto yang jauh berbeda dari kondisi alat (cahaya, sudut kamera). Enroll ulang dengan foto dari kamera alat, atau aktifkan `USE_FLASH`; cek skor di halaman Log |
| "Wajah tidak terdeteksi" padahal ada orang | wajah keluar frame (terlalu dekat < 15 cm), gelap, atau kamera menghadap salah; atur arah kamera dengan `esp32cam_stream` |
| Kamera "tidak merespons" setelah `ON` | catu 5 V kurang (kamera menyedot arus saat init); gunakan adaptor ≥ 2 A |
| ESP32 + layar mati saat 5V kamera disambung ke 5V ESP32 | brownout: USB laptop (±500 mA) tidak kuat untuk ESP32 + TFT + kamera. Beri kamera catu 5V 2A sendiri (GND tetap disatukan), atau pakai mode uji Wi-Fi (§2.6) |
| Mode Wi-Fi: `[Cam] methics-cam.local tidak ditemukan` | mDNS diblokir jaringan; isi `CAM_HOST` dengan IP kamera dari Serial Monitor kamera |
| Mode Wi-Fi: "Token kamera salah" | `CAM_TOKEN` di `secrets.h` ESP32 utama ≠ `CAM_TOKEN` di `secrets.h` kamera |
| Mode Wi-Fi: compile error `tambahkan #define CAM_TOKEN` | `secrets.h` ESP32 utama belum berisi `CAM_TOKEN` (lihat §2.6 langkah 3) |
| Mode Wi-Fi: kamera restart / `Brownout detector was triggered` (log kamera) | port USB laptop lemah untuk kamera + Wi-Fi; colok MB ke charger HP 5V 2A |
| Serial: `Gagal terhubung ke server. Kode error: -1` (ESP8266) | IP/port salah, server tidak jalan, atau Windows Firewall memblokir port 8080 |
| HTTP `401 invalid_token` | `APP_ENV=production` dan token perangkat ≠ `DEVICE_TOKEN` |
| Dashboard tetap "offline" | interval ping > 2 menit, atau jam server bergeser |
| Scan `401 invalid_signature` | jam perangkat tidak sinkron, `class_id`/`captured_at` berbeda dari yang ditandatangani, atau hash tidak dihitung atas **semua** frame berurutan |
| Scan `400 stale_capture` | `captured_at` lebih dari 5 menit dari jam server |
| Scan `403 unknown_device` | `device_id` belum ada di koleksi `devices` |
| Scan `502 face_service_error` | face service mati / `FACE_SERVICE_URL` salah |
| Kolom Foto di Log berisi `-` | penyimpanan foto nonaktif (`S3_*` kosong), Garage tidak terjangkau (lihat log `image storage: …`), scan dibuat sebelum `S3_*` diisi, atau foto sudah lewat masa retensi |
