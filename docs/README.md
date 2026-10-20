# Dokumentasi Algorithmics Face Recognation

Indeks seluruh dokumentasi proyek. Kalau baru pertama kali menyentuh proyek ini,
mulai dari **[HANDOFF.md](../HANDOFF.md)**, lalu kembali ke sini untuk detail.

| Dokumen | Isi | Untuk siapa |
| --- | --- | --- |
| [HANDOFF.md](../HANDOFF.md) | Titik masuk: status, keputusan, risiko, langkah lanjutan | Engineer/AI yang melanjutkan pekerjaan |
| [README.md](../README.md) | Cara menjalankan, konfigurasi, kredensial lokal | Siapa pun yang mau menjalankan proyek |
| [API.md](../API.md) | Kontrak API: payload JSON, skema HMAC perangkat | Integrator (perangkat ESP32, klien lain) |
| [architecture.md](architecture.md) | Arsitektur: komponen, batas kepercayaan, ADR, invarian | Perancang/peninjau teknis |
| [code-guide.md](code-guide.md) | Penjelasan kode per lapisan + resep tugas umum | Developer yang mengubah kode |
| [SRS.md](SRS.md) | Kerangka SRS (kebutuhan fungsional & non-fungsional) | Penyusun SRS / kebutuhan formal |

## Peta cepat

```text
cmd/server/main.go          entry point, routing, middleware chain, bootstrap akun
internal/api/server.go      endpoint JSON + query ber-scope role + domain method
internal/controller/        web.go (halaman), partials.go (fragment htmx), auth.go (login)
internal/model/page.go      view model halaman
internal/security/          session, principal, CSRF, rate limit, CSP, panic recovery
internal/store/             database.go (schema+seed), users.go (akun staf)
services/face/main.py       FastAPI: /health, /enroll, /verify
views/                      layouts/, pages/, partials/, fragments/, login.html
static/css|js               aset; htmx + Lucide di-vendor lokal
docs/                       dokumentasi (Anda di sini)
```

## Konvensi penulisan dokumen

- Bahasa: Indonesia, istilah teknis dipertahankan apa adanya.
- Semua klaim tentang kode dapat dilacak ke berkas/tabel; sebutkan nama berkas
  saat merujuk perilaku.
- Bedakan jelas antara **yang sudah ada di kode** dan **usulan/belum ada**.
  Dokumen di folder ini menandai status dengan `[TERSEDIA]`, `[SEBAGIAN]`,
  `[BELUM]`.
