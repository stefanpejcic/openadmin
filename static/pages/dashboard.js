function updateUserActivityTable() {
    $.ajax({
        url: '/json/combined_activity',
        type: 'GET',
        dataType: 'json',
        success: function (data) {
            // Clear existing list items
            $('#activity-list').empty();

            if (data.combined_logs.length > 0) {
                // Hide the placeholder
                $('#shouldbehidden').hide();
                
                data.combined_logs.forEach(function (log) {
                    var parts = log.split(' ');
                    var date = parts.slice(0, 3).join(' ');
                    var ip = parts[3];
                    var userAndActivity = parts.slice(4).join(' ');
                    var usernameMatch = parts[4] === 'Administrator' ? parts[5].match(/(\w+)/) : userAndActivity.match(/User (\w+)/);
                    var username = usernameMatch ? usernameMatch[1] : '';
                    var formattedDate = moment(date, 'YYYY-MM-DD HH:mm:ss').format('D.M.Y H:mm:ss');
                    var now = moment();
                    var isOnline = Math.abs(now.diff(moment(date, 'YYYY-MM-DD HH:mm:ss'), 'minutes')) <= 90;

                    var avatarClass = parts[4] === 'Administrator' 
                        ? 'bg-black dark:bg-black text-white' 
                        : isOnline 
                            ? 'bg-sky-500 dark:bg-sky-500 text-white' 
                            : 'bg-gray-50 dark:bg-gray-800 text-gray-700 dark:text-white';

                    var hreflink = parts[4] === 'Administrator' 
                        ? '/administrators' 
                        : `/users/${username}`;

                    var avatarContent = parts[4] === 'Administrator' 
                        ? '<svg xmlns="http://www.w3.org/2000/svg" class="icon icon-tabler icon-tabler-crown" width="24" height="24" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" fill="none" stroke-linecap="round" stroke-linejoin="round"><path stroke="none" d="M0 0h24v24H0z" fill="none"></path><path d="M12 6l4 6l5 -4l-2 10h-14l-2 -10l5 4z"></path></svg>' 
                        : username[0].toUpperCase();

                    var listItem = `
                        <li class="flex flex-col items-center justify-between gap-4 pl-1 py-4 sm:flex-row sm:py-3 hover:bg-gray-50 hover:dark:bg-gray-900">
                            <div class="flex w-full items-center gap-4">
                                <a href="${hreflink}">
                                    <span class="inline-flex size-9 items-center justify-center rounded-full ${avatarClass} p-1.5 text-xs font-medium ring-1 ring-gray-300 dark:ring-gray-700" aria-hidden="true">
                                        ${avatarContent}
                                    </span>
                                </a>
                                <div>
                                    <p class="text-sm font-medium text-gray-900 dark:text-gray-50"><a href="/users/${username}">${ip}</a></p>
                                    <p class="text-xs text-gray-600 dark:text-gray-400">${userAndActivity.replace(/user (\w+)/i, 'User <strong>$1</strong>')}</p>
                                </div>
                            </div>
                            <div class="flex w-full items-center gap-3 sm:w-fit">
                                <div class="text-xs text-gray-600 dark:text-gray-400">${formattedDate}</div>
                            </div>
                        </li>`;

                    $('#activity-list').append(listItem);
                });
            } else {
                $('#shouldbehidden').show();
            }
        },
        error: function (error) {
            console.error('Error fetching user activity:', error);
        }
    });
}


    // Initial call to load user activity
    updateUserActivityTable();



    // Use Ajax to get disk usage data
    $(document).ready(function() {
        $.ajax({
            url: '/json/disk',
            type: 'GET',
            dataType: 'json',
            success: function(data) {
                updateHomeUsage(data);
            }
        });
    });



// 0.3.8
function updateHomeUsage(data) {
    var homeUsage = data.find(disk_info => disk_info.mountpoint === '/');
    if (homeUsage) {
        // Update the main display of usage
        $('.home_usage').text(homeUsage.percent + '%');

        // Update the progress bar width and aria attributes
        $('#du_usage_loader')
            .css('width', homeUsage.percent + '%')
            .attr('aria-valuenow', homeUsage.percent)
            .attr('aria-label', homeUsage.percent + '%');

        // Update the visually hidden percentage display
        $('#current_disk_usage_percentage_also').text(homeUsage.percent + '%');

        // Update the total GB used value
        $('#total_gb_used').text(formatDiskSize(homeUsage.used));

        // Update the serverduIndicator background color
        var serverduIndicator = $("#serverduIndicator");
        serverduIndicator.removeClass("bg-primary-lt bg-danger-lt bg-warning-lt bg-success-lt bg-primary-lt");

        if (homeUsage.percent >= 90) {
            serverduIndicator.addClass("bg-danger-lt");
        } else if (homeUsage.percent >= 80) {
            serverduIndicator.addClass("bg-warning-lt");
        } else if (homeUsage.percent < 80) {
            serverduIndicator.addClass("bg-success-lt");
        } else {
            serverduIndicator.addClass("bg-primary-lt");
        }
    } else {
        console.log('Mountpoint "/" not found in disk usage data.');
    }
}




    // Determine the color class based on usage percentage
    function getColorClass(percentage) {
        if (percentage > 90) {
            return 'text-danger'; // Red color
        } else if (percentage > 80) {
            return 'text-warning'; // Orange color
        } else {
            return ''; // Default color
        }
    }

    // Format disk size to GB or TB
function formatDiskSize(bytes) {
    if (bytes > 1024 * 1024 * 1024 * 1024) {
        var tb = bytes / (1024 * 1024 * 1024 * 1024); // Convert to TB
        return (tb % 1 > 0.51 ? Math.ceil(tb) : Math.floor(tb)) + ' TB'; // Conditional rounding
    } else {
        var gb = bytes / (1024 * 1024 * 1024); // Convert to GB
        return (gb % 1 > 0.51 ? Math.ceil(gb) : Math.floor(gb)) + ' GB'; // Conditional rounding
    }
}















    // Function to update RAM info
function updateRamInfo() {
    $.get("/json/memory", function(data) {
        var html = data.human_readable_info.used + " / " + data.human_readable_info.total + " (" + data.human_readable_info.percent + ")";
        var percentString = data.human_readable_info.percent;
        var percent = parseInt(percentString.slice(0, -1));
        $("#human-readable-info").html(html);
        $('.ram_usage').text(percent + '%');

        updateRamChart(data.human_readable_info);

        var ramIndicator = $("#ramIndicator");
        var ramIconColor = $("#ramIconColor");
        ramIndicator.removeClass("bg-primary");
        ramIconColor.removeClass("bg-primary-lt");

        if (percent < 60) {
            ramIndicator.addClass("bg-success");
            ramIconColor.addClass("bg-success-lt");
        } else if (percent >= 60 && percent <= 80) {
            ramIndicator.addClass("bg-warning");
            ramIconColor.addClass("bg-warning-lt");
        } else {
            ramIndicator.addClass("bg-danger");
            ramIconColor.addClass("bg-danger-lt");
        }
    });
}

let chartLoad;
let chartRam;



function initRamChart() {
    chartRam = new ApexCharts(document.getElementById('chart-ram'), {
        chart: {
            type: "line",
            height: 250,
            animations: {
                enabled: false
            }
        },
        series: [
            { name: "Used RAM", data: [] },
            { name: "Total RAM", data: [] },
            { name: "RAM Usage %", data: [] }
        ],
        xaxis: {
            type: "datetime",
        },
        yaxis: {
            title: {
                text: "RAM Usage"
            }
        },
        colors: ["#007bff", "#28a745", "#ffc107"],
        tooltip: {
            theme: 'dark'
        }
    });
    chartRam.render();
}


function updateRamChart(ramData) {
    const now = new Date().getTime();
    const { used, total, percent } = ramData;

    const roundedUsed = parseFloat(used).toFixed(2);
    const roundedTotal = parseFloat(total).toFixed(2);
    const roundedPercent = parseFloat(percent).toFixed(2);

    chartRam.updateSeries([
        { name: "Used RAM", data: [...chartRam.w.config.series[0].data, [now, parseFloat(roundedUsed)]] },
        { name: "Total RAM", data: [...chartRam.w.config.series[1].data, [now, parseFloat(roundedTotal)]] },
        { name: "RAM Usage %", data: [...chartRam.w.config.series[2].data, [now, parseFloat(roundedPercent)]] }
    ]);

    // Trim data to keep the last 100 points
    chartRam.updateOptions({
        series: chartRam.w.config.series.map(series => ({
            name: series.name,
            data: series.data.slice(-100) // Keep only the last 100 points
        }))
    });
}


function initLoadChart() {
    chartLoad = new ApexCharts(document.getElementById('chart-load'), {
        chart: {
            type: "line",
            height: 250,
            animations: {
                enabled: true
            }
        },
        series: [
            { name: "1 Min Load", data: [] },
            { name: "5 Min Load", data: [] },
            { name: "15 Min Load", data: [] }
        ],
        xaxis: {
            type: "datetime",
            labels: {
                formatter: function (value) {
                    return new Date(value).toLocaleTimeString(); // Format x-axis labels to display time
                }
            }
        },
        yaxis: {
            title: {
                text: "Server Load"
            }
        },
        colors: ["#007bff", "#28a745", "#ffc107"],
        tooltip: {
            theme: 'dark',
            x: {
                formatter: function (value) {
                    return new Date(value).toLocaleTimeString(); // Format tooltip to display time
                }
            }
        }
    });
    chartLoad.render();
}


function updateLoadChart(loadData) {
    const now = new Date().getTime();
    const { load1min, load5min, load15min } = loadData;

    const roundedload1min = parseFloat(load1min).toFixed(2);
    const roundedload5min = parseFloat(load5min).toFixed(2);
    const roundedload15min = parseFloat(load15min).toFixed(2);


    chartLoad.updateSeries([
        { name: "1 Min Load", data: [...chartLoad.w.config.series[0].data, [now, parseFloat(roundedload1min)]] },
        { name: "5 Min Load", data: [...chartLoad.w.config.series[1].data, [now, parseFloat(roundedload5min)]] },
        { name: "15 Min Load", data: [...chartLoad.w.config.series[2].data, [now, parseFloat(roundedload15min)]] }
    ]);

    // Trim data to keep the last 50 points
    chartLoad.updateOptions({
        series: chartLoad.w.config.series.map(series => ({
            name: series.name,
            data: series.data.slice(-50) // Keep only the last 50 points
        }))
    });
}


function getServerLoad() {
    fetch('/json/load')
        .then(response => response.json())
        .then(loadData => {
            const load1min = parseFloat(loadData.load1min);
            const load5min = parseFloat(loadData.load5min);
            const load15min = parseFloat(loadData.load15min);
            
            document.querySelectorAll('.load1min').forEach(element => {
                element.textContent = load1min.toFixed(2);
            });
            document.getElementById('load5min').textContent = load5min.toFixed(2);
            document.getElementById('load15min').textContent = load15min.toFixed(2);

            updateLoadChart({ load1min, load5min, load15min });

            const loadDifference = load1min - load5min;

            var serverloadIndicator = $("#serverloadIndicator");
            var loadIconColor = $("#loadIconColor");
            serverloadIndicator.removeClass("bg-primary-lt");
            loadIconColor.removeClass("bg-primary-lt");

            const loadIndicator = document.getElementById('load-indicator');
            if (loadDifference >= 0.1) {
                serverloadIndicator.addClass("bg-warning");
                loadIconColor.addClass("bg-warning-lt");
                loadIndicator.innerHTML = '<span class="arrow-up">&#8593;</span>';
            } else if (loadDifference <= -0.1) {
                serverloadIndicator.addClass("bg-success");
                loadIconColor.addClass("bg-success-lt");
                loadIndicator.innerHTML = '<span class="arrow-down">&#8595;</span>';
            } else {
                serverloadIndicator.addClass("bg-primary");
                loadIconColor.addClass("bg-primary-lt");
                loadIndicator.innerHTML = '';
            }
        })
        .catch(error => console.error(error));
}

        function refreshServerLoadandRamUsage() {
            getServerLoad();
            updateRamInfo();
            setTimeout(refreshServerLoadandRamUsage, 2000);
        }

window.onload = function() {
    initLoadChart();
    initRamChart();
    refreshServerLoadandRamUsage();
};

