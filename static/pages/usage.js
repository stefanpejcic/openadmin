// --- Utility Functions ---
function formatBytes(bytes) {
    if (bytes === 0) return '0 B';
    const k = 1024, sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
}

function formatDiskSize(bytes) {
    const TB = 1024 ** 4, GB = 1024 ** 3;
    if (bytes > TB) {
        const tb = bytes / TB;
        return (tb % 1 > 0.51 ? Math.ceil(tb) : Math.floor(tb)) + ' TB';
    } else {
        const gb = bytes / GB;
        return (gb % 1 > 0.51 ? Math.ceil(gb) : Math.floor(gb)) + ' GB';
    }
}

// --- Disk Usage ---
// warnings above the tabs, each check sets or clears its own key
const usageWarnings = {};
let usageCores = 0;
function setUsageWarning(key, text) {
    if (text) usageWarnings[key] = text; else delete usageWarnings[key];
    const list = $('#usage_warnings_list').empty();
    const order = ['cpu', 'load', 'ram', 'swap', 'disk'];
    order.filter(k => usageWarnings[k]).forEach(k => list.append($('<p>').text(usageWarnings[k])));
    $('#usage_warnings').toggleClass('hidden', list.children().length === 0);
}

function updateHomeUsage(data) {
    const home = data.find(d => d.mountpoint === '/');
    if (!home) return console.log('Mountpoint "/" not found.');
    const percent = Math.round(home.percent);

    $('#current_disk_usage_percentage_also, .home_usage').text(percent + '%');
    setUsageWarning('disk', percent >= 90 ? `Disk / is ${percent}% full. Free up space or add disk before it fills up, services and databases stop when it does.` : '');

    // ring gauge, thresholds match the usage meters
    const radius = 28, circ = 2 * Math.PI * radius;
    const offset = circ - (Math.min(percent, 100) / 100) * circ;
    $('#donut_fill').css('stroke-dashoffset', offset)
        .removeClass('stroke-blue-500 stroke-yellow-500 stroke-red-500')
        .addClass(meterStroke(percent));
    $('#donut').attr('aria-valuenow', percent);
    $('#donut_percent').text(percent + '%');

    // Disk info
    $('#disk_device').text(home.device || 'N/A');
    $('#disk_fstype').text(home.fstype || 'N/A');
    $('#disk_total').text(formatDiskSize(home.total));
    $('#disk_used').text(formatDiskSize(home.used));
    $('#disk_free').text(formatDiskSize(home.free));

}

// blue below 70%, yellow from 70%, red from 90%
function meterStroke(percent) {
    return percent >= 90 ? 'stroke-red-500' : percent >= 70 ? 'stroke-yellow-500' : 'stroke-blue-500';
}

function fetchDiskUsage() {
    $.getJSON('/json/disk', updateHomeUsage);
}

// --- RAM Info ---
function updateRamInfo() {
    $.get("/json/memory", data => {
        const ram = data.human_readable.ram, swap = data.human_readable.swap;
        const ramPercent = parseInt(ram.percent);
        $("#human-readable-info").html(`${ram.used} / ${ram.total} (${ram.percent})`);
        $('.ram_usage').text(ramPercent + '%');
        const swapPercent = parseInt(swap.percent);
        setUsageWarning('ram', ramPercent >= 90 ? `RAM is ${ramPercent}% used. Find what's using it on Processes, or add RAM before processes get killed.` : '');
        setUsageWarning('swap', swapPercent >= 90 ? `Swap is ${swapPercent}% used, so the server is short on memory. Add RAM, lower memory usage or increase swap.` : '');
        $("#swap-human-readable-info").html(`${swap.used} / ${swap.total} (${swap.percent})`);
        updateRamChart(data.human_readable);
    });
}

// --- Chart theme ---
// same grid, text and series colors as charts.js, so ApexCharts follows dark mode too
const apexColors = { blue: '#3b82f6', emerald: '#10b981', violet: '#8b5cf6', amber: '#f59e0b', cyan: '#06b6d4', pink: '#ec4899', orange: '#f97316', gray: '#9ca3af' };
function escapeHtml(v) {
    return String(v).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}
// data tooltip on the popover surface, gray-500 labels and gray-900 tabular values
function apexTooltip(fmt) {
    return function ({ series, dataPointIndex, w }) {
        const x = w.globals.seriesX[0] ? w.globals.seriesX[0][dataPointIndex] : null;
        const rows = w.globals.seriesNames.map((name, i) => {
            const v = series[i] ? series[i][dataPointIndex] : null;
            if (v === null || v === undefined) return '';
            return '<div class="flex items-center justify-between gap-4"><span class="flex items-center gap-1.5 text-gray-500 dark:text-gray-500"><span class="size-2 rounded-sm" style="background:' + w.globals.colors[i] + '"></span>' + escapeHtml(name) + '</span><span class="font-medium tabular-nums text-gray-900 dark:text-gray-50">' + escapeHtml(fmt ? fmt(v) : v) + '</span></div>';
        }).join('');
        const title = x ? '<div class="mb-1 font-medium text-gray-900 dark:text-gray-50">' + new Date(x).toLocaleTimeString() + '</div>' : '';
        return '<div class="rounded-md border border-gray-200 bg-white p-2 text-xs shadow-xl shadow-black/[2.5%] dark:border-gray-800 dark:bg-gray-950">' + title + '<div class="space-y-1">' + rows + '</div></div>';
    };
}
function apexTheme() {
    const dark = document.documentElement.classList.contains('dark');
    return {
        chart: { background: 'transparent', foreColor: '#6b7280', fontFamily: 'Geist, ui-sans-serif, system-ui, sans-serif' },
        grid: { borderColor: dark ? '#1f2937' : '#e5e7eb', xaxis: { lines: { show: false } }, yaxis: { lines: { show: true } } },
        theme: { mode: dark ? 'dark' : 'light' }
    };
}
const apexLegend = { position: 'top', horizontalAlign: 'right', fontSize: '12px', markers: { width: 8, height: 8, size: 4, radius: 2, shape: 'square' } };
function themed(opts) {
    const t = apexTheme();
    return {
        ...opts,
        chart: { toolbar: { show: false }, zoom: { enabled: false }, ...opts.chart, ...t.chart },
        grid: { ...t.grid, ...opts.grid },
        theme: t.theme,
        legend: { ...apexLegend, ...opts.legend },
        dataLabels: { enabled: false },
        tooltip: { shared: true, custom: apexTooltip(opts.tooltipValue), ...opts.tooltip }
    };
}
// recolor charts already on the page when the theme switches
new MutationObserver(() => {
    [chartRam, chartLoad, chartIO, chartNet].forEach(c => { if (c) c.updateOptions(apexTheme(), false, false); });
}).observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });

// --- RAM Chart ---
let chartRam;
function initRamChart() {
    chartRam = new ApexCharts(document.getElementById('chart-ram'), themed({
        chart: { type: "area", height: 250, animations: { enabled: false }},
        series: [
            { name: "Used RAM (GB)", data: [] },
            { name: "Total RAM (GB)", data: [] }
        ],
        xaxis: { type: "datetime" },
        yaxis: { title: { text: "Memory (GB)" }},
        // total is the comparison line, so it's gray and dashed
        colors: [apexColors.blue, apexColors.gray],
        fill: { type: "solid", opacity: [0.1, 0] },
        stroke: { curve: 'smooth', width: 2, dashArray: [0, 4] }
    }));
    chartRam.render();
}

function updateRamChart(mem) {
    const now = Date.now();
    const used = parseFloat(mem.ram.used), total = parseFloat(mem.ram.total);
    chartRam.updateSeries([
        { name: "Used RAM (GB)", data: [...chartRam.w.config.series[0].data, [now, used]] },
        { name: "Total RAM (GB)", data: [...chartRam.w.config.series[1].data, [now, total]] }
    ]);
    chartRam.updateOptions({
        series: chartRam.w.config.series.map(s => ({ name: s.name, data: s.data.slice(-100) }))
    });
}

// --- Load Chart ---
let chartLoad;
function initLoadChart() {
    chartLoad = new ApexCharts(document.getElementById('chart-load'), themed({
        chart: { type: "line", height: 250, animations: { enabled: true }},
        series: [
            { name: "1 min load", data: [] },
            { name: "5 min load", data: [] },
            { name: "15 min load", data: [] }
        ],
        xaxis: {
            type: "datetime",
            labels: {
                formatter: v => new Date(v).toLocaleTimeString()
            }
        },
        yaxis: {
            title: { text: "Load" },
            labels: {
                formatter: value => parseFloat(value.toFixed(2)).toString()
            }
        },
        colors: [apexColors.blue, apexColors.emerald, apexColors.violet],
        stroke: { curve: 'smooth', width: 2 },
        tooltipValue: value => parseFloat(value.toFixed(2)).toString()
    }));
    chartLoad.render();
}


function updateLoadChart(load) {
    const now = Date.now();
    chartLoad.updateSeries([
        { name: "1 min load", data: [...chartLoad.w.config.series[0].data, [now, +load.load1min]] },
        { name: "5 min load", data: [...chartLoad.w.config.series[1].data, [now, +load.load5min]] },
        { name: "15 min load", data: [...chartLoad.w.config.series[2].data, [now, +load.load15min]] }
    ]);
    chartLoad.updateOptions({
        series: chartLoad.w.config.series.map(s => ({ name: s.name, data: s.data.slice(-50) }))
    });
}

// --- IO Chart ---
let chartIO;
function initIOChart() {
    chartIO = new ApexCharts(document.getElementById('chart-io'), themed({
        chart: { type: 'area', height: 220, animations: { enabled: false }},
        series: [
            { name: "Read MB/s", data: [] },
            { name: "Write MB/s", data: [] }
        ],
        xaxis: { type: 'datetime' },
        yaxis: { title: { text: "MB/s" }},
        colors: [apexColors.blue, apexColors.emerald],
        fill: { type: "solid", opacity: 0.1 },
        stroke: { curve: 'smooth', width: 2 }
    }));
    chartIO.render();
}
let lastIOStats = null;
let lastIOTimestamp = null;
function updateIOChart(ioData) {
    const now = Date.now();
    // Calculate delta for MB/s
    let r = 0, w = 0;
    let rps = 0, wps = 0;
    if (lastIOStats && lastIOTimestamp) {
        let dt = (now - lastIOTimestamp) / 1000.0;
        for (const k in ioData) {
            let prev = lastIOStats[k] || {};
            rps += ((ioData[k].read_bytes - (prev.read_bytes || 0)) / 1024 / 1024) / dt;
            wps += ((ioData[k].write_bytes - (prev.write_bytes || 0)) / 1024 / 1024) / dt;
        }
        r = +rps.toFixed(2);
        w = +wps.toFixed(2);
    }
    lastIOStats = JSON.parse(JSON.stringify(ioData));
    lastIOTimestamp = now;

    chartIO.updateSeries([
        { name: "Read MB/s", data: [...chartIO.w.config.series[0].data, [now, r]] },
        { name: "Write MB/s", data: [...chartIO.w.config.series[1].data, [now, w]] }
    ]);
    chartIO.updateOptions({
        series: chartIO.w.config.series.map(s => ({ name: s.name, data: s.data.slice(-50) }))
    });
}

// --- Network Chart ---
let chartNet;
function initNetChart() {
    chartNet = new ApexCharts(document.getElementById('chart-net'), themed({
        chart: { type: 'area', height: 220, animations: { enabled: false }},
        series: [
            { name: "Sent MB/s", data: [] },
            { name: "Recv MB/s", data: [] }
        ],
        xaxis: { type: 'datetime' },
        yaxis: { title: { text: "MB/s" }},
        colors: [apexColors.blue, apexColors.emerald],
        fill: { type: "solid", opacity: 0.1 },
        stroke: { curve: 'smooth', width: 2 }
    }));
    chartNet.render();
}
let lastNetStats = null;
let lastNetTimestamp = null;
function updateNetChart(netData) {
    const now = Date.now();
    let s = 0, r = 0;
    let sps = 0, rps = 0;
    if (lastNetStats && lastNetTimestamp) {
        let dt = (now - lastNetTimestamp) / 1000.0;
        for (const k in netData) {
            let prev = lastNetStats[k] || {};
            sps += ((netData[k].bytes_sent - (prev.bytes_sent || 0)) / 1024 / 1024) / dt;
            rps += ((netData[k].bytes_recv - (prev.bytes_recv || 0)) / 1024 / 1024) / dt;
        }
        s = +sps.toFixed(2);
        r = +rps.toFixed(2);
    }
    lastNetStats = JSON.parse(JSON.stringify(netData));
    lastNetTimestamp = now;

    chartNet.updateSeries([
        { name: "Sent MB/s", data: [...chartNet.w.config.series[0].data, [now, s]] },
        { name: "Recv MB/s", data: [...chartNet.w.config.series[1].data, [now, r]] }
    ]);
    chartNet.updateOptions({
        series: chartNet.w.config.series.map(s => ({ name: s.name, data: s.data.slice(-50) }))
    });
}

// --- Server Load ---
function formatDecimal(num) {
    return parseFloat(num.toFixed(2)).toString();
}

function getServerLoad() {
    fetch('/json/load').then(res => res.json()).then(load => {
        const l1 = +load.load1min, l5 = +load.load5min, l15 = +load.load15min;
        $('#load1min').text(formatDecimal(l1));
        $('#load5min').text(formatDecimal(l5));
        $('#load15min').text(formatDecimal(l15));
        updateLoadChart({ load1min: l1, load5min: l5, load15min: l15 });
        // 5 min average so a short spike doesn't warn
        const loadPercent = usageCores ? Math.round(l5 / usageCores * 100) : 0;
        setUsageWarning('load', loadPercent >= 90 ? `Load average ${formatDecimal(l5)} is ${loadPercent}% of the ${usageCores} CPU cores, processes are waiting for CPU time.` : '');

        // trend arrow, 1 min against 5 min
        const diff = l1 - l5;
        const indicator = $('#load-indicator');
        if (diff >= 0.1) {
            indicator.html('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" class="remixicon size-4"><path d="M13.0001 7.82843V20H11.0001V7.82843L5.63614 13.1924L4.22192 11.7782L12.0001 4L19.7783 11.7782L18.3641 13.1924L13.0001 7.82843Z"/></svg><span class="sr-only">rising</span>');
        } else if (diff <= -0.1) {
            indicator.html('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" class="remixicon size-4"><path d="M13.0001 16.1716L18.3641 10.8076L19.7783 12.2218L12.0001 20L4.22192 12.2218L5.63614 10.8076L11.0001 16.1716V4H13.0001V16.1716Z"/></svg><span class="sr-only">falling</span>');
        } else {
            indicator.html('');
        }
    });
}

// --- CPU Info ---
function fetchCpuUsage() {
    $.getJSON('/json/cpu', data => {
        const container = $('.container2').empty();
        let total = 0, count = 0;
        $.each(data, (core, usage) => {
            total += usage; count++;
            container.append($('<span>').addClass('size-3 rounded-sm ' + coreShade(usage)).attr('title', `Core ${core}: ${usage.toFixed(1)}%`));
        });
        const avg = count ? (total / count).toFixed(1) : 0;
        usageCores = count;
        container.attr('aria-label', `Per-core CPU load, ${count} cores, ${avg}% on average`);
        setUsageWarning('cpu', avg >= 90 ? `CPU is ${avg}% used. Check Processes for what's using it.` : '');
        $('#cpu_total').text(avg + '%');
    });
}

// blue 100 to 600 by load, idle cores go gray-800 in dark mode
function coreShade(usage) {
    if (usage < 10) return 'bg-blue-100 dark:bg-gray-800';
    if (usage < 25) return 'bg-blue-200 dark:bg-blue-900';
    if (usage < 45) return 'bg-blue-300';
    if (usage < 65) return 'bg-blue-400';
    if (usage < 85) return 'bg-blue-500';
    return 'bg-blue-600';
}

// --- IO + Network ---
function fetchIO() {
    $.getJSON('/json/io', data => {
        const tbody = $('#io-table-body').empty();
        $.each(data, (disk, stats) => {
            tbody.append(`<tr class="hover:bg-gray-50 dark:hover:bg-gray-900/60">
                <td class="px-4 py-2.5 text-sm whitespace-nowrap font-medium text-gray-900 dark:text-gray-50">${escapeHtml(disk)}</td>
                <td class="px-4 py-2.5 text-sm whitespace-nowrap text-gray-600 dark:text-gray-400 tabular-nums">${stats.read_count}</td>
                <td class="px-4 py-2.5 text-sm whitespace-nowrap text-gray-600 dark:text-gray-400 tabular-nums">${stats.write_count}</td>
                <td class="px-4 py-2.5 text-sm whitespace-nowrap text-gray-600 dark:text-gray-400 tabular-nums">${formatBytes(stats.read_bytes)}</td>
                <td class="px-4 py-2.5 text-sm whitespace-nowrap text-gray-600 dark:text-gray-400 tabular-nums">${formatBytes(stats.write_bytes)}</td>
                <td class="px-4 py-2.5 text-sm whitespace-nowrap text-gray-600 dark:text-gray-400 tabular-nums">${stats.read_time} ms</td>
                <td class="px-4 py-2.5 text-sm whitespace-nowrap text-gray-600 dark:text-gray-400 tabular-nums">${stats.write_time} ms</td>
                <td class="px-4 py-2.5 text-sm whitespace-nowrap text-gray-600 dark:text-gray-400 tabular-nums">${stats.busy_time !== null ? stats.busy_time + ' ms' : '<span class="text-gray-400 dark:text-gray-600">–</span>'}</td>
            </tr>`);
        });
        updateIOChart(data);
    }).fail(() => $('#io-table-body').html('<tr><td colspan="8" class="px-4 py-8 text-center text-sm text-gray-500 dark:text-gray-500">Couldn\'t load IO data.</td></tr>'));
}

function fetchNetwork() {
    $.getJSON('/json/network', data => {
        const tbody = $('#network-table-body').empty();
        $.each(data, (nic, stats) => {
            tbody.append(`<tr class="hover:bg-gray-50 dark:hover:bg-gray-900/60">
                <td class="px-4 py-2.5 text-sm whitespace-nowrap font-medium text-gray-900 dark:text-gray-50">${escapeHtml(nic)}</td>
                <td class="px-4 py-2.5 text-sm whitespace-nowrap text-gray-600 dark:text-gray-400 tabular-nums">${formatBytes(stats.bytes_sent)}</td>
                <td class="px-4 py-2.5 text-sm whitespace-nowrap text-gray-600 dark:text-gray-400 tabular-nums">${formatBytes(stats.bytes_recv)}</td>
                <td class="px-4 py-2.5 text-sm whitespace-nowrap text-gray-600 dark:text-gray-400 tabular-nums">${stats.packets_sent}</td>
                <td class="px-4 py-2.5 text-sm whitespace-nowrap text-gray-600 dark:text-gray-400 tabular-nums">${stats.packets_recv}</td>
                <td class="px-4 py-2.5 text-sm whitespace-nowrap text-gray-600 dark:text-gray-400 tabular-nums">${stats.errin}</td>
                <td class="px-4 py-2.5 text-sm whitespace-nowrap text-gray-600 dark:text-gray-400 tabular-nums">${stats.errout}</td>
                <td class="px-4 py-2.5 text-sm whitespace-nowrap text-gray-600 dark:text-gray-400 tabular-nums">${stats.dropin}</td>
                <td class="px-4 py-2.5 text-sm whitespace-nowrap text-gray-600 dark:text-gray-400 tabular-nums">${stats.dropout}</td>
            </tr>`);
        });
        updateNetChart(data);
    }).fail(() => $('#network-table-body').html('<tr><td colspan="9" class="px-4 py-8 text-center text-sm text-gray-500 dark:text-gray-500">Couldn\'t load network data.</td></tr>'));
}

// --- Tab Events ---
function setupTabEvents() {
    // AlpineJS recommended, fallback to click
    $('[role=tab][aria-controls]').on('click', function () {
        const tab = $(this).attr('aria-controls');
        if (tab === 'io') fetchIO();
        if (tab === 'network') fetchNetwork();
    });
}

// --- Cache and Swap Clear ---
function setupMemoryActions() {
    const csrf_token = $('meta[name="csrf-token"]').attr('content');
    $('#clear-cache').on('click', function () {
        $.ajax({
            url: '/server/memory_usage/drop',
            type: 'POST',
            headers: { 'X-CSRFToken': csrf_token },
            success: res => showToast(res.message, 'success'),
            error: xhr => showToast((xhr.responseJSON && xhr.responseJSON.message) || 'Failed to reach the server.', 'error')
        });
    });
    $('#clear-swap').on('click', function () {
        $.ajax({
            url: '/server/memory_usage/drop-swap',
            type: 'POST',
            headers: { 'X-CSRFToken': csrf_token },
            success: res => showToast(res.message, 'success'),
            error: xhr => showToast((xhr.responseJSON && xhr.responseJSON.message) || 'Failed to reach the server.', 'error')
        });
    });
}

// --- Looping Updaters ---
function refreshServerLoadAndRamUsage() {
    getServerLoad();
    updateRamInfo();
    setTimeout(refreshServerLoadAndRamUsage, 2000);
}

// --- Init ---
$(function () {
    fetchDiskUsage();
    initLoadChart();
    initRamChart();
    initIOChart();
    initNetChart();
    refreshServerLoadAndRamUsage();
    setupTabEvents();
    setupMemoryActions();
    fetchCpuUsage();
    setInterval(fetchCpuUsage, 1000);
    setInterval(fetchIO, 3000);
    setInterval(fetchNetwork, 3000);
});
