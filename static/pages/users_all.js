// INDICATE DEDI IP ADDRESS IN USERS TABLE
function updatePublicIP(username, ip) {
    const ipElements = document.querySelectorAll(`[data-username="${username}"] .public-ip`);
    ipElements.forEach(element => {
        const currentIP = element.textContent.trim();
        if (currentIP === ip) return;

        element.textContent = ip;

        const span = document.createElement('span');
        span.title = "Dedicated IP";
        span.className = "flex size-4 shrink-0 items-center justify-center rounded-full bg-blue-500 ring-2 ring-white dark:bg-blue-500 dark:ring-[#090E1A]";

        span.innerHTML = `
            <svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" width="24" height="24" fill="currentColor" aria-hidden="true" class="size-2.5 text-white dark:text-white"><path d="M11.9998 17L6.12197 20.5902L7.72007 13.8906L2.48926 9.40983L9.35479 8.85942L11.9998 2.5L14.6449 8.85942L21.5104 9.40983L16.2796 13.8906L17.8777 20.5902L11.9998 17Z"></path></svg>
        `;

        element.appendChild(span);
    });
}



// DISPLAY USER ONLINE STATUS
fetch('/json/user_activity_status')
  .then(response => response.json())
  .then(data => {
    const headerCells = document.querySelectorAll('thead th');
    let onlineColIndex = -1;
    headerCells.forEach((th, idx) => {
      const span = th.querySelector('span');
      if (span && span.textContent.trim().toLowerCase() === 'online') {
        onlineColIndex = idx;
      }
    });
    if (onlineColIndex === -1) {
      console.warn('No "online" column found');
      return;
    }

    document.querySelectorAll('tbody tr').forEach(row => {
      const usernameCell = row.querySelector('td a[href^="/users/"]');
      if (!usernameCell) return;

      const username = usernameCell.getAttribute('href').split('/').pop();
      const isActive = data[username] === 'active';

      const onlineCell = row.children[onlineColIndex];
      if (!onlineCell) return;

      onlineCell.textContent = '';

      if (isActive) {
        const dot = document.createElement('span');
        dot.classList.add('user-status-dot');
        dot.textContent = '●';
        dot.style.color = 'green';
        dot.style.fontSize = '0.9rem';
        dot.style.verticalAlign = 'middle';
        onlineCell.appendChild(dot);
      }
    });
  })
  .catch(error => console.error('Error fetching user activity status:', error));



// ROW TOOLTIPS, one delegated listener instead of a flowbite tooltip per row element
(function () {
  const tip = document.getElementById('row-tooltip');
  if (!tip) return;
  let current = null;
  function show(el) {
    current = el;
    tip.textContent = el.dataset.tip;
    tip.classList.remove('invisible', 'opacity-0');
    const r = el.getBoundingClientRect(), w = tip.offsetWidth, h = tip.offsetHeight;
    let x, y;
    switch (el.dataset.tipPlacement) {
      case 'left': x = r.left - w - 8; y = r.top + (r.height - h) / 2; break;
      case 'right': x = r.right + 8; y = r.top + (r.height - h) / 2; break;
      default: x = r.left + (r.width - w) / 2; y = r.top - h - 8;
    }
    tip.style.left = Math.max(8, Math.min(x, innerWidth - w - 8)) + 'px';
    tip.style.top = Math.max(8, y) + 'px';
  }
  function hide() {
    current = null;
    tip.classList.add('invisible', 'opacity-0');
  }
  document.addEventListener('mouseover', e => {
    const el = e.target.closest && e.target.closest('[data-tip]');
    if (el !== current) el ? show(el) : hide();
  });
  document.addEventListener('focusin', e => {
    const el = e.target.closest && e.target.closest('[data-tip]');
    if (el) show(el);
  });
  document.addEventListener('focusout', hide);
  addEventListener('scroll', hide, true);
})();



// PRIVATE NOTES, the editor is only built when a row's edit button is clicked
(function () {
  const tpl = document.getElementById('note-editor-tpl');
  if (!tpl) return;

  function close(cell) {
    cell.querySelector('.note-editor')?.remove();
    cell.querySelector('.note-view').classList.remove('hidden');
  }

  function open(cell) {
    const view = cell.querySelector('.note-view');
    const text = cell.querySelector('.note-text');
    const editor = tpl.content.firstElementChild.cloneNode(true);
    const input = editor.querySelector('.note-input');
    const save = editor.querySelector('.note-save');
    input.value = text.title;
    view.classList.add('hidden');
    cell.appendChild(editor);
    input.focus();

    save.addEventListener('click', () => {
      const note = input.value;
      save.disabled = true;
      fetch('/get_user_notes/' + cell.dataset.noteUser, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-CSRFToken': document.querySelector('meta[name="csrf-token"]').content, 'Accept': 'application/json' },
        body: JSON.stringify({ notes: note })
      }).then(r => r.json().then(data => ({ ok: r.ok, data })))
        .then(({ ok, data }) => {
          save.disabled = false;
          if (ok) {
            text.textContent = text.title = note;
            close(cell);
          }
          const message = ok ? (note.trim() === '' ? 'Note deleted successfully.' : (data.message || 'Saved successfully.')) : (data.error || 'Save failed.');
          showToast(message, ok ? 'success' : 'error');
        })
        .catch(() => { save.disabled = false; showToast('Error saving private note.', 'error'); });
    });
    editor.querySelector('.note-cancel').addEventListener('click', () => close(cell));
    input.addEventListener('keydown', e => {
      if (e.key === 'Enter') save.click();
      else if (e.key === 'Escape') close(cell);
    });
  }

  document.addEventListener('click', e => {
    const btn = e.target.closest && e.target.closest('.note-edit');
    if (btn) open(btn.closest('td[data-note-user]'));
  });
})();
