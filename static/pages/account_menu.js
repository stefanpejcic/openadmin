// ROW ACTIONS MENU for /resellers and /administrators, one menu for every row
(function () {
    const menu = document.getElementById('account-menu');
    if (!menu) return;
    const form = menu.querySelector('form');
    let current = null;

    function hide() {
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
        if (btn) return btn === current ? hide() : show(btn);
        if (!menu.contains(e.target)) hide();
    });
    document.addEventListener('keydown', e => { if (e.key === 'Escape') hide(); });
    addEventListener('scroll', hide, true);
    addEventListener('resize', hide);
})();
