// loaded in <head> so the websocket wrapper is in place before any page script runs
(function () {
    var script = document.currentScript;
    var node = new URL(script.src).searchParams.get('node') || 'local';

    // tells the master which server this tab was opened on, it refuses a mismatch
    var NativeWS = window.WebSocket;
    window.WebSocket = function (url, protocols) {
        var u = new URL(url, location.href);
        u.searchParams.set('cluster_node', node);
        return protocols === undefined ? new NativeWS(u.toString()) : new NativeWS(u.toString(), protocols);
    };
    window.WebSocket.prototype = NativeWS.prototype;
    ['CONNECTING', 'OPEN', 'CLOSING', 'CLOSED'].forEach(function (k) { window.WebSocket[k] = NativeWS[k]; });

    var CHEVRON = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" width="16" height="16" fill="currentColor" aria-hidden="true"><path d="M5.2 7.2a.75.75 0 0 1 1.06.04L10 11.1l3.74-3.86a.75.75 0 1 1 1.08 1.04l-4.28 4.42a.75.75 0 0 1-1.08 0L5.16 8.28a.75.75 0 0 1 .04-1.08z"/></svg>';

    function isDark() { return document.documentElement.classList.contains('dark'); }

    function css(el, styles) {
        Object.keys(styles).forEach(function (k) { el.style[k] = styles[k]; });
        return el;
    }

    function switchTo(id, next) {
        return fetch('/_cluster/switch', {
            method: 'POST',
            credentials: 'same-origin',
            headers: { 'Content-Type': 'application/json', 'X-Cluster-Switch': '1' },
            body: JSON.stringify({ to: id })
        }).then(function (r) {
            if (!r.ok) { throw new Error('switch failed'); }
            location.href = next || '/dashboard';
        }).catch(function () { alert('Could not switch server.'); });
    }

    window.clusterSwitchTo = switchTo;

    // the search box lists users of every server as "name (server)", this opens one on its own server
    window.clusterGo = function (serverName, path) {
        fetch('/_cluster/state', { credentials: 'same-origin' }).then(function (r) { return r.json(); }).then(function (st) {
            var s = (st.servers || []).filter(function (x) { return x.name === serverName; })[0];
            if (!s || s.id === st.active) { location.href = path; return; }
            switchTo(s.id, path);
        }).catch(function () { location.href = path; });
    };

    // same three bars as the php versions on settings > defaults, 3 green is fine, 1 red is down, all grey paused or unknown
    var BAR_COLORS = { ok: ['#10b981', '#10b981', '#10b981'], warn: ['#f97316', '#f97316', '#6b7280'], bad: ['#ef4444', '#6b7280', '#6b7280'], off: ['#6b7280', '#6b7280', '#6b7280'] };
    function statusBars(level) {
        var wrap = css(document.createElement('span'), { flex: 'none', display: 'inline-flex', gap: '2px', alignItems: 'center' });
        (BAR_COLORS[level] || BAR_COLORS.off).forEach(function (c) {
            wrap.appendChild(css(document.createElement('span'), { width: '4px', height: '14px', borderRadius: '2px', background: c }));
        });
        return wrap;
    }
    window.clusterStatusBars = statusBars;

    function statusLevel(s) {
        if (s.paused) { return 'off'; }
        // same as the servers table: offline or a core service down is red
        if (s.online === false || s.down) { return 'bad'; }
        return s.online === true || s.id === 'local' ? 'ok' : 'off';
    }

    function menuItem(label, onClick, opts) {
        opts = opts || {};
        var dark = isDark();
        var item = css(document.createElement('button'), {
            display: 'flex', alignItems: 'center', gap: '.5rem', width: '100%', padding: '.4rem .75rem', border: '0', background: 'transparent',
            textAlign: 'left', cursor: 'pointer', fontSize: '13px', lineHeight: '1.4', color: dark ? '#f9fafb' : '#111827', fontWeight: opts.active ? '600' : '400'
        });
        item.type = 'button';
        item.setAttribute('role', 'menuitem');
        if (opts.level) {
            item.appendChild(statusBars(opts.level));
        } else if (opts.color) {
            item.appendChild(css(document.createElement('span'), { flex: 'none', width: '.5rem', height: '.5rem', borderRadius: '9999px', background: opts.color }));
        }
        if (opts.paused) {
            item.style.cursor = 'default';
            item.disabled = true;
            item.title = 'Paused, resume it under Manage servers';
        }
        var text = css(document.createElement('span'), { flex: '1', minWidth: '0' });
        var name = css(document.createElement('span'), { display: 'block', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' });
        name.textContent = label;
        text.appendChild(name);
        if (opts.sub) {
            var sub = css(document.createElement('span'), { display: 'block', fontSize: '11px', color: '#6b7280' });
            sub.textContent = opts.sub;
            text.appendChild(sub);
        }
        item.appendChild(text);
        if (opts.active) {
            var check = css(document.createElement('span'), { flex: 'none' });
            check.textContent = '✓';
            item.appendChild(check);
        }
        item.addEventListener('mouseenter', function () { if (!item.disabled) { item.style.background = dark ? '#1f2937' : '#f3f4f6'; } });
        item.addEventListener('mouseleave', function () { item.style.background = 'transparent'; });
        item.addEventListener('click', onClick);
        return item;
    }

    function buildMenu(state) {
        var dark = isDark();
        // fixed and on body because the sidebar header clips its overflow
        var menu = css(document.createElement('div'), {
            position: 'fixed', zIndex: '100', minWidth: '15rem', maxWidth: '20rem', maxHeight: '70vh', overflowY: 'auto', padding: '.25rem 0', borderRadius: '.5rem',
            background: dark ? '#111827' : '#fff', border: '1px solid ' + (dark ? '#1f2937' : '#e5e7eb'), boxShadow: '0 10px 25px rgba(0,0,0,.18)', display: 'none'
        });
        menu.id = 'cluster-menu';
        menu.setAttribute('role', 'menu');
        state.servers.forEach(function (s) {
            var sub = s.address || '';
            if (s.id === 'local') { sub += ' · master'; }
            if (s.paused) { sub += ' · paused'; } else if (s.online === false) { sub += ' · offline'; } else if (s.down) { sub += ' · service down'; }
            if (s.unread) { sub += ' · ' + s.unread + ' unread'; }
            menu.appendChild(menuItem(s.name, function () {
                if (s.id === state.active) { closeMenu(menu); return; }
                switchTo(s.id);
            }, { active: s.id === state.active, level: statusLevel(s), sub: sub, paused: s.paused }));
        });
        menu.appendChild(css(document.createElement('div'), { height: '1px', margin: '.25rem 0', background: dark ? '#1f2937' : '#e5e7eb' }));
        if (state.unsynced > 0) {
            menu.appendChild(menuItem(state.unsynced + ' unsynced setting(s)', function () { switchTo('local', '/server/cluster#sync'); }, { color: '#d97706' }));
        }
        menu.appendChild(menuItem('Manage servers', function () { switchTo('local', '/server/cluster'); }));
        document.body.appendChild(menu);
        return menu;
    }

    function closeMenu(menu) { menu.style.display = 'none'; }

    function openMenu(menu, anchor) {
        var box = anchor.getBoundingClientRect();
        menu.style.display = 'block';
        menu.style.top = (box.bottom + 6) + 'px';
        menu.style.left = Math.max(8, box.left) + 'px';
    }

    function bindToggle(trigger, menu) {
        trigger.setAttribute('aria-haspopup', 'menu');
        trigger.addEventListener('click', function (e) {
            e.preventDefault();
            e.stopPropagation();
            if (menu.style.display === 'none') { openMenu(menu, trigger); } else { closeMenu(menu); }
        });
        document.addEventListener('click', function (e) {
            if (!menu.contains(e.target)) { closeMenu(menu); }
        });
        document.addEventListener('keydown', function (e) {
            if (e.key === 'Escape') { closeMenu(menu); }
        });
    }

    // the sidebar header becomes the switcher, so the server you're on is always named in the same spot
    function renderInSidebar(state, current, menu) {
        var host = document.getElementById('hostname');
        var row = host && host.closest('[data-sidebar="header"] > div');
        if (!row) { return false; }
        var remote = state.active !== 'local';
        host.textContent = current.name;
        css(row, { cursor: 'pointer', borderRadius: '.375rem', width: '100%' });
        row.setAttribute('title', 'Switch server');
        var label = host.closest('div');
        css(label, { flex: '1', minWidth: '0', overflow: 'hidden' });
        css(host, { display: 'block', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' });
        if (remote) {
            var sub = label.querySelector('span.block.text-xs');
            if (sub) {
                sub.textContent = 'Remote · ' + (current.address || '');
                css(sub, { color: '#d97706', fontWeight: '500' });
            }
        }
        var chevron = css(document.createElement('span'), { flex: 'none', display: 'flex', opacity: '.7' });
        chevron.innerHTML = CHEVRON;
        row.appendChild(chevron);
        if (state.unsynced > 0) {
            chevron.appendChild(css(document.createElement('span'), { width: '.45rem', height: '.45rem', borderRadius: '9999px', background: '#d97706', alignSelf: 'center' }));
        }
        bindToggle(row, menu);
        return true;
    }

    // pages without the sidebar, like the "server is down" one, still need a way out
    function renderStandalone(current, menu) {
        var btn = css(document.createElement('button'), {
            display: 'inline-flex', alignItems: 'center', gap: '.4rem', margin: '1rem', padding: '.4rem .75rem', border: '1px solid #d1d5db',
            borderRadius: '.375rem', background: '#fff', color: '#111827', cursor: 'pointer', fontSize: '13px'
        });
        btn.type = 'button';
        btn.textContent = current.name;
        var chevron = css(document.createElement('span'), { display: 'flex' });
        chevron.innerHTML = CHEVRON;
        btn.appendChild(chevron);
        document.body.insertBefore(btn, document.body.firstChild);
        bindToggle(btn, menu);
    }

    // results from every server under the regular search box, opening one switches to its server
    function bindSearch() {
        var input = document.getElementById('searchInput');
        var list = document.getElementById('filteredDropdown');
        if (!input || !list) { return; }
        var timer, seq = 0;
        function clear() { list.querySelectorAll('.cluster-hit').forEach(function (el) { el.remove(); }); }
        input.addEventListener('input', function () {
            clearTimeout(timer);
            var q = input.value.trim();
            var mine = ++seq;
            clear();
            if (q.length < 2) { return; }
            timer = setTimeout(function () {
                fetch('/_cluster/search?q=' + encodeURIComponent(q), { credentials: 'same-origin' })
                    .then(function (r) { return r.ok ? r.json() : []; })
                    .then(function (hits) {
                        if (mine !== seq) { return; }
                        clear();
                        hits.forEach(function (h) {
                            if (h.kind === 'account') {
                                // this server's accounts are in the box's own list already, they just get the server name too
                                if (h.server !== 'local') { list.appendChild(accountHit(h)); return; }
                                var own = list.querySelector('a[href="' + h.link + '"] span.text-sm');
                                if (own && !own.querySelector('.cluster-server')) {
                                    var where = css(document.createElement('span'), { display: 'block', fontSize: '11px', color: '#6b7280' });
                                    where.className = 'cluster-server';
                                    where.textContent = h.name;
                                    own.appendChild(where);
                                }
                                return;
                            }
                            var li = document.createElement('li');
                            li.className = 'cluster-hit p-1 border-b border-gray-200 dark:border-gray-800';
                            var a = css(document.createElement('a'), { display: 'block', fontSize: '13px', cursor: 'pointer' });
                            a.href = h.link;
                            a.textContent = h.label;
                            var sub = css(document.createElement('span'), { display: 'block', fontSize: '11px', color: '#6b7280' });
                            sub.textContent = h.kind + ' on ' + h.name;
                            a.appendChild(sub);
                            a.addEventListener('click', function (e) {
                                e.preventDefault();
                                switchTo(h.server, h.link);
                            });
                            li.appendChild(a);
                            list.appendChild(li);
                        });
                        if (hits.length) { list.style.display = ''; }
                    }).catch(function () {});
            }, 450);
        });
    }

    // same icons as the search box's own user rows in the layout
    var PERSON_ICON = "<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"22\" height=\"22\" fill=\"currentColor\" class=\"bi bi-person-fill\" viewBox=\"0 0 16 16\"><path d=\"M3 14s-1 0-1-1 1-4 6-4 6 3 6 4-1 1-1 1zm5-6a3 3 0 1 0 0-6 3 3 0 0 0 0 6\"/></svg>";
    var LOGIN_CLASS = "relative inline-flex items-center justify-center rounded border p-1 text-center text-sm font-medium shadow-sm transition-all duration-100 ease-in-out cursor-pointer outline outline-offset-2 outline-0 focus-visible:outline-2 outline-blue-500 dark:outline-blue-500 dark:border-gray-800 dark:text-gray-50 dark:bg-gray-950 dark:hover:bg-gray-900/60 disabled:text-gray-400 disabled:dark:text-gray-600 sm:w-fit border-black bg-black text-white hover:bg-gray-800 sm:mt-0";
    var LOGIN_ICON = "<svg version=\"1.0\" style=\"vertical-align:middle;\" xmlns=\"http://www.w3.org/2000/svg\" width=\"16\" height=\"18\" viewBox=\"0 0 213 215\" preserveAspectRatio=\"xMidYMid meet\"><g transform=\"translate(0,215) scale(0.1,-0.1)\" fill=\"currentColor\" stroke=\"none\"><path d=\"M990 2071 c-39 -13 -141 -66 -248 -129 -53 -32 -176 -103 -272 -158 -206 -117 -276 -177 -306 -264 -17 -50 -19 -88 -19 -460 0 -476 0 -474 94 -568 55 -56 124 -98 604 -369 169 -95 256 -104 384 -37 104 54 532 303 608 353 76 50 126 113 147 184 8 30 12 160 12 447 0 395 -1 406 -22 461 -34 85 -98 138 -317 264 -104 59 -237 136 -295 170 -153 90 -194 107 -275 111 -38 2 -81 0 -95 -5z m205 -561 c66 -38 166 -95 223 -127 l102 -58 0 -262 c0 -262 0 -263 -22 -276 -13 -8 -52 -31 -88 -51 -36 -21 -126 -72 -200 -115 l-135 -78 -3 261 -3 261 -166 95 c-91 52 -190 109 -219 125 -30 17 -52 34 -51 39 3 9 424 256 437 255 3 0 59 -31 125 -69z\"/></g></svg>";

    // an account of another server looks like the box's own user rows, with the server under the name
    function accountHit(h) {
        var user = h.link.replace(/^\/users\//, '');
        var li = document.createElement('li');
        li.className = 'cluster-hit dropdown-item';
        var row = css(document.createElement('div'), { display: 'flex', alignItems: 'center', justifyContent: 'space-between', width: '100%' });
        var a = css(document.createElement('a'), { display: 'flex', alignItems: 'center', gap: '.5rem', cursor: 'pointer' });
        a.href = h.link;
        a.insertAdjacentHTML('afterbegin', PERSON_ICON);
        var text = document.createElement('span');
        text.className = 'text-sm';
        text.textContent = user;
        var sub = css(document.createElement('span'), { display: 'block', fontSize: '11px', color: '#6b7280' });
        sub.textContent = h.name;
        text.appendChild(sub);
        a.appendChild(text);
        a.addEventListener('click', function (e) { e.preventDefault(); switchTo(h.server, h.link); });
        row.appendChild(a);
        var login = document.createElement('a');
        login.className = LOGIN_CLASS;
        login.innerHTML = LOGIN_ICON;
        login.href = '/login/token/' + user;
        login.title = 'Login as ' + user + ' into OpenPanel';
        login.addEventListener('click', function (e) { e.preventDefault(); switchTo(h.server, '/login/token/' + user); });
        row.appendChild(login);
        li.appendChild(row);
        return li;
    }

    // the page's own sort links and search don't know about ?server=, keep it across them
    function keepServerFilter() {
        var only = new URLSearchParams(location.search).get('server');
        var main = document.getElementById('no_margin_for_iframe');
        if (!only || !main) { return; }
        main.querySelectorAll('a[href]').forEach(function (a) {
            var u = new URL(a.href, location.href);
            if (u.origin === location.origin && u.pathname === location.pathname && !u.searchParams.has('server')) {
                u.searchParams.set('server', only);
                a.setAttribute('href', u.pathname + u.search);
            }
        });
        document.querySelectorAll('form').forEach(function (f) {
            if ((f.getAttribute('method') || 'get').toLowerCase() !== 'get' || f.closest('#sidebar')) { return; }
            var hidden = document.createElement('input');
            hidden.type = 'hidden';
            hidden.name = 'server';
            hidden.value = only;
            f.appendChild(hidden);
        });
    }

    // the add form's domain can live on another server, the form then posts there with that server's token
    function bindEmailForm() {
        var form = document.querySelector('form[action="/emails/new"]');
        if (!form) { return; }
        form.addEventListener('submit', function () {
            var sel = form.querySelector('select[name="domain"]');
            var opt = sel && sel.selectedOptions[0];
            if (opt && opt.dataset.action) {
                form.setAttribute('action', opt.dataset.action);
                form.querySelector('input[name="csrf_token"]').value = opt.dataset.token;
            }
        }, true);
    }

    function closeDropdownsOnClickAway() {
        document.addEventListener('click', function (e) {
            document.querySelectorAll('details.cluster-dd[open]').forEach(function (d) {
                if (!d.contains(e.target)) { d.removeAttribute('open'); }
            });
        });
    }

    // the box only counts for what's submitted from this page, the master replays those posts on the other servers
    function bindApplyAll() {
        function save(on) {
            document.cookie = 'cluster_apply_all=' + (on ? '1; max-age=600' : '; max-age=0') + '; path=/; SameSite=Strict; Secure';
        }
        var boxes = document.querySelectorAll('.cluster-apply-all');
        save(boxes.length > 0 && boxes[0].checked);
        boxes.forEach(function (box) {
            box.addEventListener('change', function () { save(box.checked); });
        });
        // a box inside a form only speaks for that form, the page's other forms stay on this server
        if (document.querySelector('form .cluster-apply-all')) {
            document.addEventListener('submit', function (e) {
                var own = e.target.querySelector('.cluster-apply-all');
                save(!!own && own.checked);
            }, true);
        }
    }

    // notices sit in a strip at the bottom of the window, lined up with the page next to the sidebar
    function bottomStrip() {
        var strip = document.getElementById('cluster-bottom');
        if (strip) { return strip; }
        var main = document.getElementById('no_margin_for_iframe') || document.body;
        strip = css(document.createElement('div'), { position: 'fixed', bottom: '0', right: '0', left: main.getBoundingClientRect().left + 'px', zIndex: '40', display: 'flex', flexDirection: 'column', boxShadow: '0 -1px 3px rgba(0,0,0,.08)' });
        strip.id = 'cluster-bottom';
        document.body.appendChild(strip);
        new ResizeObserver(function () { strip.style.left = main.getBoundingClientRect().left + 'px'; }).observe(main);
        return strip;
    }

    function showApplied(state) {
        if (state.applied && state.applied.length) {
            var bad = state.applied.filter(function (a) { return !a.ok; });
            var note = css(document.createElement('div'), { padding: '.5rem 1rem', fontSize: '13px', cursor: 'pointer', background: bad.length ? '#fee2e2' : '#dcfce7', color: bad.length ? '#7f1d1d' : '#14532d' });
            note.title = 'Dismiss';
            note.textContent = state.applied.map(function (a) { return a.server + ': ' + (a.note || (a.ok ? 'applied' : 'failed, ' + a.error)); }).join('  |  ');
            note.addEventListener('click', function () { note.remove(); });
            var strip = bottomStrip();
            strip.insertBefore(note, strip.firstChild);
        }
        if (state.apply_pending) {
            setTimeout(function () {
                fetch('/_cluster/state', { credentials: 'same-origin' }).then(function (r) { return r.ok ? r.json() : null; }).then(function (next) { if (next) { showApplied(next); } }).catch(function () {});
            }, 2000);
        }
    }

    // a member as destination hides the ssh fields, the move itself runs from the master
    function bindTransferPicker() {
        var box = document.getElementById('cluster-transfer');
        if (!box) { return; }
        var select = document.getElementById('cluster-transfer-to');
        var member = document.getElementById('cluster-transfer-member');
        var result = document.getElementById('cluster-transfer-result');
        select.addEventListener('change', function () {
            member.style.display = select.value ? '' : 'none';
            Array.prototype.forEach.call(box.parentElement.children, function (el) {
                if (el !== box) { el.style.display = select.value ? 'none' : ''; }
            });
        });
        document.getElementById('cluster-transfer-start').addEventListener('click', function () {
            result.textContent = 'Starting...';
            fetch('/_cluster/move', {
                method: 'POST',
                credentials: 'same-origin',
                headers: { 'Content-Type': 'application/json', 'X-Cluster-Switch': '1' },
                body: JSON.stringify({ account: box.dataset.account, from: box.dataset.from, to: select.value, live: document.getElementById('cluster-transfer-live').checked, reseller: document.getElementById('cluster-transfer-reseller').value })
            }).then(function (r) {
                if (r.ok) {
                    result.textContent = 'Move started, follow it under Manage servers.';
                    return;
                }
                return r.text().then(function (t) { result.textContent = t; });
            }).catch(function () { result.textContent = 'Could not start the move.'; });
        });
    }

    // a page's "..." menu is one element filled in from the row that opened it, for another server's row its actions must go there
    function bindRowMenus() {
        var lastBtn = null;
        ['mouseover', 'click'].forEach(function (type) {
            document.addEventListener(type, function (e) {
                var btn = e.target.closest && e.target.closest('[class*="-menu-btn"]');
                if (btn) { lastBtn = btn; }
            }, true);
        });
        function remoteNode(target) {
            var menu = target.closest && target.closest('[id$="-menu"][role="menu"]');
            return menu && lastBtn && lastBtn.dataset.clusterNode ? lastBtn.dataset.clusterNode : '';
        }
        document.addEventListener('click', function (e) {
            var node = remoteNode(e.target);
            if (!node) { return; }
            var link = e.target.closest('a[href]');
            var action = e.target.closest('[data-action]');
            if (link && link.getAttribute('href').charAt(0) === '/') {
                e.preventDefault();
                e.stopImmediatePropagation();
                switchTo(node, link.getAttribute('href'));
            } else if (action) {
                // these open a confirm dialog of the page, that dialog lives on the row's server
                e.preventDefault();
                e.stopImmediatePropagation();
                switchTo(node, location.pathname + location.search);
            }
        }, true);
        document.addEventListener('submit', function (e) {
            var node = remoteNode(e.target);
            if (!node) { return; }
            e.preventDefault();
            e.stopImmediatePropagation();
            var meta = document.querySelector('meta[name="csrf-token"]');
            fetch('/_cluster/n/' + encodeURIComponent(node) + e.target.getAttribute('action'), {
                method: 'POST',
                credentials: 'same-origin',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'X-CSRFToken': meta ? meta.content : '' },
                body: new URLSearchParams(new FormData(e.target))
            }).then(function () { location.reload(); }).catch(function () { alert('Could not reach that server.'); });
        }, true);
    }

    function render(state) {
        bindSearch();
        bindRowMenus();
        bindTransferPicker();
        bindApplyAll();
        showApplied(state);
        bindEmailForm();
        closeDropdownsOnClickAway();
        keepServerFilter();
        var current = state.servers.find(function (s) { return s.id === state.active; }) || state.servers[0];
        var menu = buildMenu(state);
        if (!renderInSidebar(state, current, menu)) { renderStandalone(current, menu); }

        if (state.active === 'local' && state.unsynced > 0 && location.pathname.indexOf('/settings/') === 0) {
            var note = css(document.createElement('div'), { padding: '.4rem .9rem', fontSize: '13px', background: '#fef3c7', color: '#78350f' });
            note.id = 'cluster-unsynced';
            note.textContent = state.unsynced + ' setting(s) changed on the master since the last sync. ';
            var link = document.createElement('a');
            link.href = '/server/cluster#sync';
            link.textContent = 'Review and sync to other servers';
            link.style.textDecoration = 'underline';
            note.appendChild(link);
            bottomStrip().appendChild(note);
        }
    }

    document.addEventListener('DOMContentLoaded', function () {
        // page warnings marked for it move to the strip at the bottom of the window
        document.querySelectorAll('[data-cluster-bottom]').forEach(function (el) {
            el.classList.remove('mb-6', 'rounded');
            css(el, { margin: '0', padding: '.4rem .9rem', fontSize: '13px' });
            bottomStrip().appendChild(el);
        });
        fetch('/_cluster/state', { credentials: 'same-origin' })
            .then(function (r) { return r.ok ? r.json() : null; })
            .then(function (state) { if (state && state.servers && state.servers.length) { render(state); } })
            .catch(function () {});
    });
})();
