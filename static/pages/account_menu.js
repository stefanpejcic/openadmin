// ROW ACTIONS MENU for /resellers and /administrators, one menu for every row
(function () {
    const menu = document.getElementById('account-menu');
    if (!menu) return;
    const form = menu.querySelector('form');
    let current = null;

    // visible items, the ones this row can't run are hidden
    const items = () => [...menu.querySelectorAll('a[href], button')].filter(el => !el.closest('.hidden'));

    function hide(refocus) {
        if (current) current.setAttribute('aria-expanded', 'false');
        if (refocus && current) current.focus();
        menu.classList.add('hidden');
        current = null;
    }

    function show(btn) {
        current = btn;
        const user = btn.dataset.user;
        const actions = (btn.dataset.actions || '').split(' ').filter(Boolean);
        menu.querySelectorAll('[data-href]').forEach(a => {
            a.href = a.dataset.href.replace('{user}', encodeURIComponent(user));
        });
        menu.querySelectorAll('[data-post]').forEach(b => {
            b.closest('li').classList.toggle('hidden', !actions.includes(b.dataset.post));
        });

        menu.classList.remove('hidden');
        const r = btn.getBoundingClientRect(), w = menu.offsetWidth, h = menu.offsetHeight;
        let y = r.bottom + 4;
        if (y + h > innerHeight - 8) y = Math.max(8, r.top - h - 4);
        menu.style.left = Math.max(8, Math.min(r.right - w, innerWidth - w - 8)) + 'px';
        menu.style.top = y + 'px';
        btn.setAttribute('aria-expanded', 'true');
        const first = items()[0];
        if (first) first.focus();
    }

    menu.addEventListener('click', e => {
        const b = e.target.closest('[data-post]');
        if (!b || !current) return;
        form.elements.action.value = b.dataset.post;
        form.elements.username.value = current.dataset.user;
        form.submit();
    });
    document.addEventListener('click', e => {
        const btn = e.target.closest('.account-menu-btn');
        if (btn) {
            if (btn === current) return hide();
            if (current) current.setAttribute('aria-expanded', 'false');
            return show(btn);
        }
        if (!menu.contains(e.target)) hide();
    });
    document.addEventListener('keydown', e => {
        if (!current) return;
        if (e.key === 'Escape') return hide(true);
        if (e.key === 'Tab') return hide();
        if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
        e.preventDefault();
        const list = items(), at = list.indexOf(document.activeElement);
        list[(at + (e.key === 'ArrowDown' ? 1 : list.length - 1)) % list.length].focus();
    });
    document.querySelectorAll('.account-menu-btn').forEach(b => {
        b.setAttribute('aria-haspopup', 'menu');
        b.setAttribute('aria-expanded', 'false');
    });
    addEventListener('scroll', hide, true);
    addEventListener('resize', hide);
})();
