// shared by every page of _layout.html, loaded deferred right before Alpine so the alpine:init listeners are in place
// resource bar polling in the header, only on pages that show it
(function () {
if (!document.getElementById("sse_cpu")) return;
// a stale or broken cache from an older version must not take the rest of this file down with it
let cachedData = {};
try { cachedData = JSON.parse(localStorage.getItem("sseData") || "{}") || {}; } catch (e) { cachedData = {}; }

function formatBytes(bytes) {
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    if (bytes === 0) return '0 B';
    const i = Math.floor(Math.log(bytes) / Math.log(1024));
    return (bytes / Math.pow(1024, i)).toFixed(2) + ' ' + sizes[i];
}

// label on the left, value on the right
function tipRow(label, value) {
    return `<div class="flex justify-between gap-6 py-0.5 whitespace-nowrap"><span class="text-gray-500">${label}</span><span class="font-medium tabular-nums text-gray-900 dark:text-gray-50">${value}</span></div>`;
}

// heading line for data tooltips with several rows
function tipTitle(text) {
    return `<p class="mb-1 font-medium text-gray-900 dark:text-gray-50">${text}</p>`;
}

const tipArrow = "";

function updateSSEUI(data) {
    if (data.cpu !== undefined) {
        const cpuTooltip = document.getElementById("tooltip-sse_cpu");
        // a master gets every member's average and shows the busiest one
        if (Array.isArray(data.cpu.servers)) {
            const servers = [...data.cpu.servers].sort((a, b) => b.avg - a.avg);
            document.getElementById("sse_cpu").textContent = (servers.length ? servers[0].avg : 0).toFixed(1) + "%";
            cpuTooltip.innerHTML = tipTitle("CPU usage") + servers.map(s => tipRow(s.name, `${s.avg.toFixed(1)}% (${s.cores} ${s.cores === 1 ? 'core' : 'cores'})`)).join("") + tipArrow;
        } else {
            const cpuValues = Object.values(data.cpu);
            document.getElementById("sse_cpu").textContent = (cpuValues.reduce((a,b)=>a+b,0)/cpuValues.length).toFixed(1) + "%";
            cpuTooltip.innerHTML = tipTitle("CPU usage per core") +
                Object.entries(data.cpu)
                      .sort(([, a], [, b]) => b - a)
                      .map(([core, val]) => tipRow(core.replace(/_/g, ' '), `${val.toFixed(1)}%`))
                      .join("") + tipArrow;
        }
    }

    if (data.memory !== undefined) {
        const mem = data.memory.human_readable;
        document.getElementById("sse_memory").textContent = mem.ram.percent;
        const memTooltip = document.getElementById("tooltip-sse_memory");
        if (data.memory.servers) {
            const servers = [...data.memory.servers].sort((a, b) => parseFloat(b.ram.percent) - parseFloat(a.ram.percent));
            memTooltip.innerHTML = tipTitle("RAM usage") + servers.map(s => tipRow(s.name, `${s.ram.used} / ${s.ram.total} (${s.ram.percent})`)).join("") + tipArrow;
        } else {
            memTooltip.innerHTML = tipRow("RAM", `${mem.ram.used} / ${mem.ram.total} (${mem.ram.percent})`) + tipRow("Swap", `${mem.swap.used} / ${mem.swap.total} (${mem.swap.percent})`) + tipArrow;
        }
    }

    if (data.load !== undefined) {
        document.getElementById("sse_load").textContent = data.load.load1min.toFixed(2);
        const loadTooltip = document.getElementById("tooltip-sse_load");
        if (data.load.servers) {
            const servers = [...data.load.servers].sort((a, b) => b.load1min - a.load1min);
            loadTooltip.innerHTML = tipTitle("Load (1, 5, 15 min)") + servers.map(s => tipRow(s.name, `${s.load1min.toFixed(2)} / ${s.load5min.toFixed(2)} / ${s.load15min.toFixed(2)}`)).join("") + tipArrow;
        } else {
            loadTooltip.innerHTML = tipRow("Load 1 min", data.load.load1min.toFixed(2)) + tipRow("Load 5 min", data.load.load5min.toFixed(2)) + tipRow("Load 15 min", data.load.load15min.toFixed(2)) + tipArrow;
        }
    }

    if (data.disk !== undefined) {
        const rootDisk = data.disk.find(d => d.mountpoint === "/");

        if (rootDisk) {
            document.getElementById("sse_disk").textContent = rootDisk.percent.toFixed(1) + "%";
        }

        const diskTooltip = document.getElementById("tooltip-sse_disk");
        // a master's first entry is only the number shown in the header
        if (data.disk[0] && data.disk[0].device === "Fullest server") {
            const servers = data.disk.slice(1).sort((a, b) => b.percent - a.percent);
            diskTooltip.innerHTML = tipTitle("Disk usage") + servers.map(d => tipRow(d.device, `${formatBytes(d.used)} / ${formatBytes(d.total)} (${d.percent}%)`)).join("") + tipArrow;
        } else {
            const filteredDisks = data.disk.filter(d => !d.mountpoint.startsWith("/mnt")).sort((a, b) => b.percent - a.percent);
            diskTooltip.innerHTML = filteredDisks.map(d => tipRow(`${d.device} on ${d.mountpoint}`, `${formatBytes(d.used)} / ${formatBytes(d.total)} (${d.percent}%)`)).join("") + tipArrow;
        }
    }

}

if (Object.keys(cachedData).length) {
    try { updateSSEUI(cachedData); } catch (e) { cachedData = {}; localStorage.removeItem("sseData"); }
}

const resourceIntervals = { cpu: 15 * 1000, memory: 30 * 1000, load: 15 * 60 * 1000, disk: 5 * 60 * 1000 };
const resourceFetchedAt = {};

function fetchResource(resource) {
    if (document.hidden) return;
    resourceFetchedAt[resource] = Date.now();
    fetch(`/json/${resource}?cluster=all`)
        .then(res => res.json())
        .then(data => {
            updateSSEUI({[resource]: data});
            cachedData[resource] = data;
            localStorage.setItem("sseData", JSON.stringify(cachedData));
        })
        .catch(err => console.error(`Error fetching ${resource}:`, err));
}

window.addEventListener("load", () => {
    for (const [resource, ms] of Object.entries(resourceIntervals)) {
        setInterval(() => fetchResource(resource), ms);
        fetchResource(resource);
    }
});

// background tabs skip the polls, so catch up on whatever went stale once the tab is active again
document.addEventListener("visibilitychange", () => {
    if (document.hidden) return;
    for (const [resource, ms] of Object.entries(resourceIntervals)) {
        if (Date.now() - (resourceFetchedAt[resource] || 0) >= ms) fetchResource(resource);
    }
});
})();

// the "..." row menus open on hover, each page keeps one shared menu per table
(function () {
    let hideTimer = null;
    let openBtn = null;
    function menuFor(btn) {
        const match = btn.className.match(/([\w-]+)-menu-btn/);
        return match ? document.getElementById(match[1] + '-menu') : null;
    }
    // hover already opened it, a real click on the same button would toggle it shut again
    document.addEventListener('click', e => {
        const btn = e.isTrusted && e.target.closest && e.target.closest('[class*="-menu-btn"]');
        const menu = btn && menuFor(btn);
        if (btn && btn === openBtn && menu && !menu.classList.contains('hidden')) {
            e.stopImmediatePropagation();
        }
    }, true);
    document.addEventListener('mouseover', e => {
        const btn = e.target.closest && e.target.closest('[class*="-menu-btn"]');
        if (btn) {
            clearTimeout(hideTimer);
            const menu = menuFor(btn);
            if (menu && (menu.classList.contains('hidden') || openBtn !== btn)) {
                btn.click();
                openBtn = btn;
            }
            return;
        }
        if (openBtn) {
            const menu = menuFor(openBtn);
            if (menu && menu.contains(e.target)) {
                clearTimeout(hideTimer);
                return;
            }
            clearTimeout(hideTimer);
            const leaving = openBtn;
            hideTimer = setTimeout(() => {
                const m = menuFor(leaving);
                if (m && !m.classList.contains('hidden')) document.body.click();
                openBtn = null;
            }, 250);
        }
    });
})();

// auto-dismiss progress bar of flash messages
    document.addEventListener('DOMContentLoaded', () => {
        const flashes = document.querySelectorAll('.op-flash');
        const alertEl = flashes[0];
        const progressBar = document.getElementById('alert-progress-bar');
        const progressContainer = document.getElementById('alert-progress-container');

        if (alertEl && progressBar && progressContainer) {
            const duration = 10000;
            const step = 10;
            let progress = 0;
            let isPaused = false;
            let interval;

            const startProgress = () => {
                interval = setInterval(() => {
                    if (!isPaused) {
                        progress += step;
                        let percent = (progress / duration) * 100;
                        progressBar.style.width = `${percent}%`;

                        if (progress >= duration) {
                            clearInterval(interval);
                            flashes.forEach(el => el.remove());
                            progressContainer.remove();
                        }
                    }
                }, step);
            };

            alertEl.addEventListener('mouseenter', () => {
                isPaused = true;
            });

            alertEl.addEventListener('mouseleave', () => {
                isPaused = false;
            });

            startProgress();
        }
    });

// keyboard shortcuts
document.addEventListener('alpine:init', () => {
  Alpine.data('openadminShortcuts', () => ({
    shortcuts: null,
    showHelp: false,

    async init() {
      if (this.shortcuts) return;
      try {
        const res = await fetch('/shortcuts.json', { cache: 'no-store' });
        if (!res.ok) return;

        const data = await res.json();
        if (!data || typeof data !== 'object') return;

        this.shortcuts = data;
        window.addEventListener('keydown', this.handleKeydown.bind(this));
      } catch {
        return;
      }
    },

    handleKeydown(event) {
      if (
        ['INPUT', 'TEXTAREA'].includes(event.target.tagName) ||
        event.target.isContentEditable
      ) return;

      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        event.stopPropagation();
        this.showHelp = true;
        return;
      }

      const combo = this.normalize(event);
      const target = this.shortcuts?.[combo];

      if (!target) return;

      event.preventDefault();
      window.location.href = target;
    },

    normalize(event) {
      const keys = [];
      if (event.ctrlKey || event.metaKey) keys.push('ctrl');
      if (event.shiftKey) keys.push('shift');
      if (event.altKey) keys.push('alt');
      keys.push(event.key.toLowerCase());
      return keys.join('+');
    }
  }));
});

// sidebar toggle
function toggleSidebar(){const root=document.documentElement;const isOpen=root.classList.toggle('sidebar-open');sessionStorage.setItem('sidebarOpen',isOpen);}window.addEventListener('resize',()=>{if(window.innerWidth<768){document.documentElement.classList.remove('sidebar-open');}});

// ask before leaving a settings form with unsaved changes
(function(){let dirty=false;const mark=e=>{if(e.target.closest&&e.target.closest('form[data-warn-unsaved]'))dirty=true;};document.addEventListener('input',mark);document.addEventListener('change',mark);document.addEventListener('submit',()=>{dirty=false;},true);window.addEventListener('beforeunload',e=>{if(dirty){e.preventDefault();e.returnValue='';}});})();

// dark mode toggle
(function(){const D=document.getElementById('theme-toggle-dark-icon'),L=document.getElementById('theme-toggle-light-icon'),B=document.getElementById('theme-toggle');if(!D||!L||!B)return;(localStorage.getItem('color-theme')==='dark'||(!('color-theme' in localStorage)&&window.matchMedia('(prefers-color-scheme: dark)').matches))?L.classList.remove('hidden'):D.classList.remove('hidden');B.addEventListener('click',function(){D.classList.toggle('hidden');L.classList.toggle('hidden');const t=localStorage.getItem('color-theme'),d=document.documentElement.classList;t?(t==='light'?(d.add('dark'),localStorage.setItem('color-theme','dark')):(d.remove('dark'),localStorage.setItem('color-theme','light'))):(d.contains('dark')?(d.remove('dark'),localStorage.setItem('color-theme','light')):(d.add('dark'),localStorage.setItem('color-theme','dark')))});})();

// tooltips and dismiss buttons for pages that don't load flowbite
document.addEventListener('DOMContentLoaded',function(){if(typeof window.initTooltips==='function')return;function place(t,tip,p){var r=t.getBoundingClientRect(),w=tip.offsetWidth,h=tip.offsetHeight,x,y;if(p==='left'){x=r.left-w-8;y=r.top+(r.height-h)/2}else if(p==='right'){x=r.right+8;y=r.top+(r.height-h)/2}else if(p==='bottom'){x=r.left+(r.width-w)/2;y=r.bottom+8}else{x=r.left+(r.width-w)/2;y=r.top-h-8}x=Math.max(8,Math.min(x,innerWidth-w-8));y=Math.max(8,y);tip.style.position='fixed';tip.style.left=x+'px';tip.style.top=y+'px'}document.querySelectorAll('[data-tooltip-target]').forEach(function(t){var tip=document.getElementById(t.getAttribute('data-tooltip-target'));if(!tip)return;tip.style.position='fixed';tip.style.left='0';tip.style.top='0';var timer;function show(){clearTimeout(timer);tip.style.visibility='visible';tip.style.opacity='1';tip.style.pointerEvents='none';place(t,tip,t.getAttribute('data-tooltip-placement')||'top')}function later(){clearTimeout(timer);timer=setTimeout(show,200)}function hide(){clearTimeout(timer);tip.style.visibility='hidden';tip.style.opacity='0'}t.addEventListener('mouseenter',later);t.addEventListener('focus',show);t.addEventListener('mouseleave',hide);t.addEventListener('blur',hide);t.addEventListener('keydown',function(e){if(e.key==='Escape')hide()})});document.addEventListener('click',function(e){var b=e.target.closest&&e.target.closest('[data-dismiss-target]');if(!b)return;var sel=b.getAttribute('data-dismiss-target'),el=b.closest(sel)||document.querySelector(sel);if(el){el.style.transition='opacity .3s';el.style.opacity='0';setTimeout(function(){el.style.display='none'},300)}})});

// product tour, driver.js only loads when the tour starts
(function(){var loading=null;function css(h){var l=document.createElement('link');l.rel='stylesheet';l.href=h;document.head.appendChild(l);}function js(src){return new Promise(function(ok,fail){var s=document.createElement('script');s.src=src;s.onload=ok;s.onerror=fail;document.head.appendChild(s);});}function load(){if(!loading){css('/static/dist/css/driverjs131.min.css');loading=js('/static/dist/js/driverjs131.min.js').then(function(){return js('/static/dist/js/tour-steps.js');}).then(function(){return js('/static/dist/js/tour.js');});}return loading;}window.OPTour={start:function(f){load().then(function(){window.OPTour.start(f);});},markComplete:function(){var t=document.querySelector('meta[name="csrf-token"]');fetch('/api/tour/complete',{method:'POST',headers:t?{'X-CSRFToken':t.content}:{}}).catch(function(){});}};if(localStorage.getItem('op_tour_active')==='1')load();})();

// branded stand-in for window.confirm, resolves true or false
window.opConfirm = function (message, opts) {
    opts = opts || {};
    const text = String(message || '').trim();
    const verb = (text.match(/^(delete|remove|kill|disable|clear|leave|detach|stop|reset|drop|terminate|restart|cancel)\b/i) || [])[1];
    const danger = opts.danger !== undefined ? opts.danger : !!verb;
    const okLabel = opts.ok || (verb ? verb.charAt(0).toUpperCase() + verb.slice(1).toLowerCase() : 'Continue');
    const btn = 'relative inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-md border px-3 py-2 text-center text-sm font-medium shadow-xs transition-all duration-100 ease-in-out outline outline-0 outline-offset-2 outline-blue-500 focus-visible:outline-2';
    const okClass = danger
        ? 'border-transparent bg-red-600 text-white hover:bg-red-700 dark:bg-red-700 dark:hover:bg-red-600'
        : 'border-transparent bg-blue-500 text-white hover:bg-blue-600';
    const icon = danger
        ? '<span class="flex size-10 shrink-0 items-center justify-center rounded-full bg-red-50 text-red-600 dark:bg-red-500/10 dark:text-red-500"><svg xmlns="http://www.w3.org/2000/svg" class="remixicon size-5" aria-hidden="true" viewBox="0 0 24 24" fill="currentColor"><path d="M12.8659 3.00017L22.3922 19.5002C22.6684 19.9785 22.5045 20.5901 22.0262 20.8662C21.8742 20.954 21.7017 21.0002 21.5262 21.0002H2.47363C1.92135 21.0002 1.47363 20.5525 1.47363 20.0002C1.47363 19.8246 1.51984 19.6522 1.60761 19.5002L11.1339 3.00017C11.41 2.52187 12.0216 2.358 12.4999 2.63414C12.6519 2.72191 12.7782 2.84815 12.8659 3.00017ZM4.20568 19.0002H19.7941L11.9999 5.50017L4.20568 19.0002ZM10.9999 16.0002H12.9999V18.0002H10.9999V16.0002ZM10.9999 9.00017H12.9999V14.0002H10.9999V9.00017Z"/></svg></span>'
        : '';
    return new Promise(function (resolve) {
        const prev = document.activeElement;
        const wrap = document.createElement('div');
        wrap.className = 'fixed inset-0 z-[60] flex items-center justify-center bg-black/30 p-4';
        wrap.setAttribute('role', 'alertdialog');
        wrap.setAttribute('aria-modal', 'true');
        wrap.setAttribute('aria-labelledby', 'op-confirm-title');
        wrap.setAttribute('aria-describedby', 'op-confirm-text');
        wrap.innerHTML =
            '<div class="w-full max-w-md rounded-md border border-gray-200 bg-white shadow-lg dark:border-gray-900 dark:bg-raised">' +
            '<div class="flex gap-4 px-6 pt-6">' + icon + '<div class="min-w-0">' +
            '<h2 id="op-confirm-title" class="text-base font-semibold text-gray-900 dark:text-gray-50"></h2>' +
            '<p id="op-confirm-text" class="mt-1 text-sm break-words text-gray-500 dark:text-gray-500"></p></div></div>' +
            '<div class="mt-6 flex flex-col-reverse gap-2 border-t border-gray-200 px-6 py-4 sm:flex-row sm:justify-end dark:border-gray-900">' +
            '<button type="button" data-act="cancel" class="' + btn + ' border-gray-300 bg-white text-gray-900 hover:bg-gray-50 dark:border-gray-800 dark:bg-gray-950 dark:text-gray-50 dark:hover:bg-gray-900/60">Cancel</button>' +
            '<button type="button" data-act="ok" class="' + btn + ' ' + okClass + '"></button></div></div>';
        wrap.querySelector('#op-confirm-title').textContent = opts.title || (danger ? 'Are you sure?' : 'Please confirm');
        wrap.querySelector('#op-confirm-text').textContent = text;
        const ok = wrap.querySelector('[data-act=ok]');
        const cancel = wrap.querySelector('[data-act=cancel]');
        ok.textContent = okLabel;
        function close(result) {
            document.removeEventListener('keydown', onKey, true);
            wrap.remove();
            if (prev && prev.focus) prev.focus();
            resolve(result);
        }
        function onKey(e) {
            if (e.key === 'Escape') { e.preventDefault(); close(false); }
            if (e.key === 'Tab') {
                e.preventDefault();
                (document.activeElement === ok ? cancel : ok).focus();
            }
        }
        ok.addEventListener('click', function () { close(true); });
        cancel.addEventListener('click', function () { close(false); });
        wrap.addEventListener('mousedown', function (e) { if (e.target === wrap) close(false); });
        document.addEventListener('keydown', onKey, true);
        document.body.appendChild(wrap);
        (danger ? cancel : ok).focus();
    });
};

// data-confirm on a form or its submit button asks before the form goes out
document.addEventListener('submit', function (e) {
    const form = e.target;
    const sub = e.submitter;
    const msg = (sub && sub.getAttribute('data-confirm')) || form.getAttribute('data-confirm');
    if (!msg) return;
    if (form.__opConfirmed) { form.__opConfirmed = false; return; }
    e.preventDefault();
    e.stopImmediatePropagation();
    window.opConfirm(msg).then(function (yes) {
        if (!yes) return;
        form.__opConfirmed = true;
        if (form.requestSubmit) form.requestSubmit(sub || undefined); else form.submit();
    });
}, true);

// data-dropdown-toggle menus without flowbite: click opens the target under its button, outside click or Esc closes it
(function () {
    let open = null;
    function close() {
        if (!open) return;
        open.menu.classList.add('hidden');
        open.btn.setAttribute('aria-expanded', 'false');
        open = null;
    }
    function place(btn, menu) {
        const r = btn.getBoundingClientRect();
        menu.style.position = 'fixed';
        menu.style.zIndex = '50';
        menu.style.margin = '0';
        const w = menu.offsetWidth, h = menu.offsetHeight;
        let left = r.left;
        if (left + w > innerWidth - 8) left = Math.max(8, r.right - w);
        let top = r.bottom + 4;
        if (top + h > innerHeight - 8 && r.top - h - 4 > 8) top = r.top - h - 4;
        menu.style.left = left + 'px';
        menu.style.top = top + 'px';
    }
    document.querySelectorAll('[data-dropdown-toggle]').forEach(function (btn) {
        btn.setAttribute('aria-haspopup', 'true');
        btn.setAttribute('aria-expanded', 'false');
    });
    document.addEventListener('click', function (e) {
        const btn = e.target.closest('[data-dropdown-toggle]');
        if (btn) {
            const menu = document.getElementById(btn.getAttribute('data-dropdown-toggle'));
            if (!menu) return;
            e.preventDefault();
            const same = open && open.menu === menu;
            close();
            if (same) return;
            menu.classList.remove('hidden');
            place(btn, menu);
            btn.setAttribute('aria-expanded', 'true');
            open = { btn: btn, menu: menu };
            return;
        }
        if (open && !open.menu.contains(e.target)) close();
    });
    document.addEventListener('keydown', function (e) { if (e.key === 'Escape' && open) { const b = open.btn; close(); b.focus(); } });
    window.addEventListener('resize', close);
    window.addEventListener('scroll', function (e) { if (open && !open.menu.contains(e.target)) close(); }, true);
})();
