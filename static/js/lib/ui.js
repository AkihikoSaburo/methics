// Komponen UI bersama: notifikasi, badge, bar progres, dan sel tabel.
export function refreshIcons() {
  if (window.lucide) window.lucide.createIcons();
}

export function toast(message, error = false) {
  const element = document.querySelector('#toast');
  if (!element) return;
  element.textContent = message;
  element.classList.toggle('error', error);
  element.hidden = false;
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => { element.hidden = true; }, 4200);
}

export function setText(selector, value) {
  const element = document.querySelector(selector);
  if (element) element.textContent = value;
}

export function cell(text) {
  const element = document.createElement('td');
  element.textContent = text || '-';
  return element;
}

export function emptyRow(columns, text) {
  const row = document.createElement('tr');
  const cellElement = document.createElement('td');
  cellElement.colSpan = columns;
  cellElement.textContent = text;
  row.append(cellElement);
  return row;
}

export function notice(text) {
  const element = document.createElement('div');
  element.className = 'role-note';
  element.textContent = text;
  return element;
}

export function studentIdentity(student) {
  const cellElement = document.createElement('td');
  const employee = document.createElement('div');
  employee.className = 'employee';
  const avatar = document.createElement('span');
  avatar.className = 'avatar';
  avatar.textContent = (student.name.match(/\b\p{L}/gu) || []).slice(0, 2).join('').toUpperCase();
  const copy = document.createElement('span');
  const name = document.createElement('span');
  name.className = 'employee-name';
  name.textContent = student.name || 'Mahasiswa';
  const nim = document.createElement('span');
  nim.className = 'employee-mail';
  nim.textContent = `NIM ${student.nim || '-'}`;
  copy.append(name, nim);
  employee.append(avatar, copy);
  cellElement.append(employee);
  return cellElement;
}

export function statusBadge(status) {
  const span = document.createElement('span');
  span.className = 'status';
  const normalized = (status || '').toLowerCase();
  if (normalized === 'hadir') span.classList.add('present');
  else if (normalized === 'perlu ditinjau') span.classList.add('late');
  else if (normalized === 'ditolak') span.classList.add('absent');
  span.textContent = status || '-';
  return span;
}

export function progressBar(percent) {
  const wrap = document.createElement('div');
  wrap.className = 'progress';
  const bar = document.createElement('span');
  bar.style.width = `${clampPercent(percent)}%`;
  wrap.append(bar);
  return wrap;
}

export function progressTrack(percent) {
  const track = document.createElement('div');
  track.className = 'dept-track';
  const bar = document.createElement('span');
  bar.style.width = `${clampPercent(percent)}%`;
  track.append(bar);
  return track;
}

export function deptRow(name, value, percent, icon) {
  const row = document.createElement('div');
  row.className = 'dept-row';
  const nameElement = document.createElement('div');
  nameElement.className = 'dept-name';
  if (icon) {
    const iconElement = document.createElement('i');
    iconElement.dataset.lucide = icon;
    nameElement.append(iconElement);
  }
  nameElement.append(document.createTextNode(name));
  const valueElement = document.createElement('div');
  valueElement.className = 'dept-value';
  valueElement.textContent = value;
  row.append(nameElement, valueElement, progressTrack(percent));
  return row;
}

function clampPercent(percent) {
  return Math.max(0, Math.min(100, Number(percent) || 0));
}
