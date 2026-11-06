// shared by every page of _layout.html, loaded deferred right before Alpine so the alpine:init listeners are in place
// resource bar polling in the header, only on pages that show it
(function () {
if (!document.getElementById("sse_cpu")) return;
const cachedData = JSON.parse(localStorage.getItem("sseData") || "{}");

function formatBytes(bytes) {
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    if (bytes === 0) return '0 B';
    const i = Math.floor(Math.log(bytes) / Math.log(1024));
    return (bytes / Math.pow(1024, i)).toFixed(2) + ' ' + sizes[i];
}

// label on the left, value on the right
function tipRow(label, value) {
    return `<div style="display:flex;justify-content:space-between;gap:1.5rem;white-space:nowrap"><span>${label}</span><span>${value}</span></div>`;
}

const tipArrow = `<div class="tooltip-arrow" data-popper-arrow></div>`;

function updateSSEUI(data) {
    if (data.cpu !== undefined) {
        const cpuTooltip = document.getElementById("tooltip-sse_cpu");
        // a master gets every member's average and shows the busiest one
        if (Array.isArray(data.cpu.servers)) {
            const servers = [...data.cpu.servers].sort((a, b) => b.avg - a.avg);
            document.getElementById("sse_cpu").textContent = (servers.length ? servers[0].avg : 0).toFixed(1) + "%";
            cpuTooltip.innerHTML = "CPU usage:" + servers.map(s => tipRow(s.name, `${s.avg.toFixed(1)}% (${s.cores} ${s.cores === 1 ? 'core' : 'cores'})`)).join("") + tipArrow;
        } else {
            const cpuValues = Object.values(data.cpu);
            document.getElementById("sse_cpu").textContent = (cpuValues.reduce((a,b)=>a+b,0)/cpuValues.length).toFixed(1) + "%";
            cpuTooltip.innerHTML = "CPU usage per core:" +
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
            memTooltip.innerHTML = "RAM usage:" + servers.map(s => tipRow(s.name, `${s.ram.used} / ${s.ram.total} (${s.ram.percent})`)).join("") + tipArrow;
        } else {
            memTooltip.innerHTML = tipRow("RAM", `${mem.ram.used} / ${mem.ram.total} (${mem.ram.percent})`) + tipRow("Swap", `${mem.swap.used} / ${mem.swap.total} (${mem.swap.percent})`) + tipArrow;
        }
    }

    if (data.load !== undefined) {
        document.getElementById("sse_load").textContent = data.load.load1min.toFixed(2);
        const loadTooltip = document.getElementById("tooltip-sse_load");
        if (data.load.servers) {
            const servers = [...data.load.servers].sort((a, b) => b.load1min - a.load1min);
            loadTooltip.innerHTML = "Load (1, 5, 15 min):" + servers.map(s => tipRow(s.name, `${s.load1min.toFixed(2)} / ${s.load5min.toFixed(2)} / ${s.load15min.toFixed(2)}`)).join("") + tipArrow;
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
            diskTooltip.innerHTML = "Disk usage:" + servers.map(d => tipRow(d.device, `${formatBytes(d.used)} / ${formatBytes(d.total)} (${d.percent}%)`)).join("") + tipArrow;
        } else {
            const filteredDisks = data.disk.filter(d => !d.mountpoint.startsWith("/mnt")).sort((a, b) => b.percent - a.percent);
            diskTooltip.innerHTML = filteredDisks.map(d => tipRow(`${d.device} on ${d.mountpoint}`, `${formatBytes(d.used)} / ${formatBytes(d.total)} (${d.percent}%)`)).join("") + tipArrow;
        }
    }

}

if (Object.keys(cachedData).length) {
    updateSSEUI(cachedData);
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
        const alertEl = document.getElementById('alert');
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
                            alertEl.remove();
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
document.addEventListener('DOMContentLoaded',function(){if(typeof window.initTooltips==='function')return;function place(t,tip,p){var r=t.getBoundingClientRect(),w=tip.offsetWidth,h=tip.offsetHeight,x,y;if(p==='left'){x=r.left-w-8;y=r.top+(r.height-h)/2}else if(p==='right'){x=r.right+8;y=r.top+(r.height-h)/2}else if(p==='bottom'){x=r.left+(r.width-w)/2;y=r.bottom+8}else{x=r.left+(r.width-w)/2;y=r.top-h-8}x=Math.max(8,Math.min(x,innerWidth-w-8));y=Math.max(8,y);tip.style.position='fixed';tip.style.left=x+'px';tip.style.top=y+'px'}document.querySelectorAll('[data-tooltip-target]').forEach(function(t){var tip=document.getElementById(t.getAttribute('data-tooltip-target'));if(!tip)return;function show(){tip.style.visibility='visible';tip.style.opacity='1';tip.style.pointerEvents='none';place(t,tip,t.getAttribute('data-tooltip-placement')||'top')}function hide(){tip.style.visibility='hidden';tip.style.opacity='0'}t.addEventListener('mouseenter',show);t.addEventListener('focus',show);t.addEventListener('mouseleave',hide);t.addEventListener('blur',hide)});document.addEventListener('click',function(e){var b=e.target.closest&&e.target.closest('[data-dismiss-target]');if(!b)return;var sel=b.getAttribute('data-dismiss-target'),el=b.closest(sel)||document.querySelector(sel);if(el){el.style.transition='opacity .3s';el.style.opacity='0';setTimeout(function(){el.style.display='none'},300)}})});

// product tour, driver.js only loads when the tour starts
(function(){var loading=null;function css(h){var l=document.createElement('link');l.rel='stylesheet';l.href=h;document.head.appendChild(l);}function js(src){return new Promise(function(ok,fail){var s=document.createElement('script');s.src=src;s.onload=ok;s.onerror=fail;document.head.appendChild(s);});}function load(){if(!loading){css('/static/dist/css/driverjs131.min.css');loading=js('/static/dist/js/driverjs131.min.js').then(function(){return js('/static/dist/js/tour-steps.js');}).then(function(){return js('/static/dist/js/tour.js');});}return loading;}window.OPTour={start:function(f){load().then(function(){window.OPTour.start(f);});},markComplete:function(){var t=document.querySelector('meta[name="csrf-token"]');fetch('/api/tour/complete',{method:'POST',headers:t?{'X-CSRFToken':t.content}:{}}).catch(function(){});}};if(localStorage.getItem('op_tour_active')==='1')load();})();
