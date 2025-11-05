
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
function updateHomeUsage(data) {
    const home = data.find(d => d.mountpoint === '/');
    if (!home) return console.log('Mountpoint "/" not found.');
    const percent = Math.round(home.percent);

    $('#current_disk_usage_percentage_also, .home_usage').text(percent + '%');

    // Donut chart visual
    const radius = 45, circ = 2 * Math.PI * radius;
    const offset = circ - (percent / 100) * circ;
    $('#donut_fill').css('stroke-dashoffset', offset)
        .removeClass('text-green-500 text-yellow-500 text-red-500')
        .addClass(percent >= 90 ? 'text-red-500' : percent >= 80 ? 'text-yellow-500' : 'text-green-500');
    $('#donut_percent').text(percent + '%');

    // Disk info
    $('#disk_device').text(home.device || 'N/A');
    $('#disk_fstype').text(home.fstype || 'N/A');
    $('#disk_total').text(formatDiskSize(home.total));
    $('#disk_used').text(formatDiskSize(home.used));
    $('#disk_free').text(formatDiskSize(home.free));

    // Progress bar
    $('#tailwind_progress_fill').css('width', percent + '%')
        .removeClass('bg-red-500 bg-orange-400 bg-green-500')
        .addClass(percent >= 90 ? 'bg-red-500' : percent >= 80 ? 'bg-orange-500' : 'bg-green-500');

    // Disk indicator
    $("#serverduIndicator").removeClass("text-red-500 text-orange-500 text-emerald-500 text-gray-500")
        .addClass(percent >= 90 ? "text-red-500" : percent >= 80 ? "text-orange-500" : percent < 80 ? "text-gray-500" : "text-gray-500");
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
        $("#swap-human-readable-info").html(`${swap.used} / ${swap.total} (${swap.percent})`);
        updateRamChart(data.human_readable);
    });
}

// --- RAM Chart ---
let chartRam;
function initRamChart() {
    chartRam = new ApexCharts(document.getElementById('chart-ram'), {
        chart: { type: "area", height: 250, animations: { enabled: false }},
        series: [
            { name: "Used RAM (GB)", data: [] },
            { name: "Total RAM (GB)", data: [] }
        ],
        xaxis: { type: "datetime" },
        yaxis: { title: { text: "Memory Usage (GB)" }},
        colors: ["#007bff", "#28a745"],
        fill: { type: "gradient", gradient: { shadeIntensity: 1, opacityFrom: 0.4, opacityTo: 0.05, stops: [0, 90, 100] }},
        stroke: { curve: 'smooth', width: 2 },
        tooltip: { theme: 'dark' }
    });
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
    chartLoad = new ApexCharts(document.getElementById('chart-load'), {
        chart: { type: "line", height: 250, animations: { enabled: true }},
        series: [
            { name: "1 Min Load", data: [] },
            { name: "5 Min Load", data: [] },
            { name: "15 Min Load", data: [] }
        ],
        xaxis: {
            type: "datetime",
            labels: {
                formatter: v => new Date(v).toLocaleTimeString()
            }
        },
        yaxis: {
            title: { text: "Server Load" },
            labels: {
                formatter: value => parseFloat(value.toFixed(2)).toString()
            }
        },
        colors: ["#007bff", "#28a745", "#ffc107"],
        tooltip: {
            theme: 'dark',
            x: {
                formatter: v => new Date(v).toLocaleTimeString()
            },
            y: {
                formatter: value => parseFloat(value.toFixed(2)).toString()
            }
        }
    });
    chartLoad.render();
}


function updateLoadChart(load) {
    const now = Date.now();
    chartLoad.updateSeries([
        { name: "1 Min Load", data: [...chartLoad.w.config.series[0].data, [now, +load.load1min]] },
        { name: "5 Min Load", data: [...chartLoad.w.config.series[1].data, [now, +load.load5min]] },
        { name: "15 Min Load", data: [...chartLoad.w.config.series[2].data, [now, +load.load15min]] }
    ]);
    chartLoad.updateOptions({
        series: chartLoad.w.config.series.map(s => ({ name: s.name, data: s.data.slice(-50) }))
    });
}

// --- IO Chart ---
let chartIO;
function initIOChart() {
    chartIO = new ApexCharts(document.getElementById('chart-io'), {
        chart: { type: 'area', height: 220, animations: { enabled: false }},
        series: [
            { name: "Read MB/s", data: [] },
            { name: "Write MB/s", data: [] }
        ],
        xaxis: { type: 'datetime' },
        yaxis: { title: { text: "MB/s" }},
        colors: ["#00bcd4", "#ff9800"],
        fill: { type: "gradient", gradient: { shadeIntensity: 1, opacityFrom: 0.4, opacityTo: 0.05, stops: [0, 90, 100] }},
        stroke: { curve: 'smooth', width: 2 },
        tooltip: { theme: 'dark' }
    });
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
    chartNet = new ApexCharts(document.getElementById('chart-net'), {
        chart: { type: 'area', height: 220, animations: { enabled: false }},
        series: [
            { name: "Sent MB/s", data: [] },
            { name: "Recv MB/s", data: [] }
        ],
        xaxis: { type: 'datetime' },
        yaxis: { title: { text: "MB/s" }},
        colors: ["#4caf50", "#e91e63"],
        fill: { type: "gradient", gradient: { shadeIntensity: 1, opacityFrom: 0.4, opacityTo: 0.05, stops: [0, 90, 100] }},
        stroke: { curve: 'smooth', width: 2 },
        tooltip: { theme: 'dark' }
    });
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
        $('.load1min').text(formatDecimal(l1));
        $('#load5min').text(formatDecimal(l5));
        $('#load15min').text(formatDecimal(l15));
        updateLoadChart({ load1min: l1, load5min: l5, load15min: l15 });

        // Load indicator/arrows
        const diff = l1 - l5;
        $('#serverloadIndicator, #loadIconColor').removeClass("bg-primary-lt bg-primary bg-warning bg-warning-lt bg-success bg-success-lt");
        const indicator = $('#load-indicator');
        if (diff >= 0.1) {
            $('#serverloadIndicator').addClass("bg-warning");
            $('#loadIconColor').addClass("bg-warning-lt");
            indicator.html('<span class="arrow-up">&#8593;</span>');
        } else if (diff <= -0.1) {
            $('#serverloadIndicator').addClass("bg-success");
            $('#loadIconColor').addClass("bg-success-lt");
            indicator.html('<span class="arrow-down">&#8595;</span>');
        } else {
            $('#serverloadIndicator').addClass("bg-primary");
            $('#loadIconColor').addClass("bg-primary-lt");
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
            const color = usage >= 90 ? 'crimson' : usage >= 80 ? 'antiquewhite' : '#f0f8ff';
            container.append($('<div>').css('background-color', color).append(`<span>${usage.toFixed(1)}%</span>`));
        });
        const avg = count ? (total / count).toFixed(1) : 0;
        $('#cpu_avg-now').removeClass("text-red-500 text-orange-500 text-emerald-500 text-gray-500")
            .addClass(avg >= 90 ? "text-red-500" : avg >= 80 ? "text-orange-500" : avg >= 50 ? "text-gray-500" : "text-gray-500")
            .text(avg + '%');
        $('#cpu_total').text(avg + '%');
    });
}

// --- IO + Network ---
function fetchIO() {
    $.getJSON('/json/io', data => {
        const tbody = $('#io-table-body').empty();
        $.each(data, (disk, stats) => {
            tbody.append(`<tr>
                <td class="px-3 py-2">${disk}</td>
                <td class="px-3 py-2">${stats.read_count}</td>
                <td class="px-3 py-2">${stats.write_count}</td>
                <td class="px-3 py-2">${formatBytes(stats.read_bytes)}</td>
                <td class="px-3 py-2">${formatBytes(stats.write_bytes)}</td>
                <td class="px-3 py-2">${stats.read_time} ms</td>
                <td class="px-3 py-2">${stats.write_time} ms</td>
                <td class="px-3 py-2">${stats.busy_time !== null ? stats.busy_time + ' ms' : '-'}</td>
            </tr>`);
        });
        updateIOChart(data);
    }).fail(() => $('#io-table-body').html('<tr><td colspan="8">Error loading IO data.</td></tr>'));
}

function fetchNetwork() {
    $.getJSON('/json/network', data => {
        const tbody = $('#network-table-body').empty();
        $.each(data, (nic, stats) => {
            tbody.append(`<tr>
                <td class="px-3 py-2">${nic}</td>
                <td class="px-3 py-2">${formatBytes(stats.bytes_sent)}</td>
                <td class="px-3 py-2">${formatBytes(stats.bytes_recv)}</td>
                <td class="px-3 py-2">${stats.packets_sent}</td>
                <td class="px-3 py-2">${stats.packets_recv}</td>
                <td class="px-3 py-2">${stats.errin}</td>
                <td class="px-3 py-2">${stats.errout}</td>
                <td class="px-3 py-2">${stats.dropin}</td>
                <td class="px-3 py-2">${stats.dropout}</td>
            </tr>`);
        });
        updateNetChart(data);
    }).fail(() => $('#network-table-body').html('<tr><td colspan="9">Error loading network data.</td></tr>'));
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
            error: xhr => showToast(xhr.responseJSON.message, 'error')
        });
    });
    $('#clear-swap').on('click', function () {
        $.ajax({
            url: '/server/memory_usage/drop-swap',
            type: 'POST',
            headers: { 'X-CSRFToken': csrf_token },
            success: res => showToast(res.message, 'success'),
            error: xhr => showToast(xhr.responseJSON.message, 'error')
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
