// Entry point frontend.
// Seluruh halaman sudah dirender server + htmx, jadi tidak ada lagi modul
// per-halaman di sini; file ini hanya menyiapkan htmx dan perilaku global.
import { refreshIcons, toast } from './lib/ui.js';

refreshIcons();

// Integrasi htmx: matikan eval (kompatibel dengan CSP script-src 'self'),
// sisipkan token CSRF pada setiap request htmx, dan segarkan ikon Lucide
// setelah fragment HTML ditukar.
const htmx = window.htmx;
if (htmx) {
  htmx.config.allowEval = false;
  document.body.addEventListener('htmx:configRequest', (event) => {
    const csrfToken = document.querySelector('meta[name="csrf-token"]')?.content;
    if (csrfToken) event.detail.headers['X-CSRF-Token'] = csrfToken;
  });
  document.body.addEventListener('htmx:responseError', (event) => {
    if (event.detail.xhr.status === 401) window.location.assign('/login');
  });
  const syncActiveNavigation = () => {
    const currentPath = window.location.pathname;
    document.querySelectorAll('.sidebar > .nav-list .nav-link').forEach((link) => {
      const active = link.pathname === currentPath && link.hash === '';
      link.classList.toggle('active', active);
      if (active) link.setAttribute('aria-current', 'page');
      else link.removeAttribute('aria-current');
    });
  };
  document.body.addEventListener('htmx:afterSwap', () => {
    refreshIcons();
    syncActiveNavigation();
    document.body.dataset.page = document.querySelector('#main-content')?.dataset.page ?? document.body.dataset.page;
  });
  document.body.addEventListener('htmx:historyRestore', syncActiveNavigation);
}

// Event yang dipancarkan server lewat header HX-Trigger.
document.body.addEventListener('toast', (event) => {
  toast(event.detail?.message ?? '', Boolean(event.detail?.error));
});
document.body.addEventListener('student-dialog-close', () => {
  document.querySelector('#studentDialog')?.close();
});

// Delegasi klik global supaya tetap bekerja untuk markup yang ditukar htmx.
document.addEventListener('click', (event) => {
  if (event.target.closest('#menuToggle')) {
    document.querySelector('#sidebar')?.classList.toggle('open');
    return;
  }
  if (event.target.closest('#printReport')) {
    window.print();
    return;
  }
  if (event.target.closest('#openStudentDialog')) {
    document.querySelector('#studentDialog')?.showModal();
    return;
  }
  if (event.target.closest('#closeStudentDialog, #cancelStudentDialog')) {
    document.querySelector('#studentDialog')?.close();
  }
});
