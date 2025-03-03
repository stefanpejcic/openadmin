$(document).ready(function() {

// SERVICES STATUS
function fetchServices() {
    $.ajax({
        url: '/json/services-status',
        type: 'GET',
        dataType: 'json',
        success: function (data) {
            var table = '<table class="table table-striped"><thead><tr><th>Service</th><th>Status</th><th>Actions</th></tr></thead><tbody>';

            data.forEach(function (service) {
                var displayName = service.name;
                var status = service.status;
                var actionsHtml = '';

                if (status === 'Inactive') {
                    actionsHtml = '<a href="/service/start/' + service.real_name + '" data-bs-toggle="tooltip" data-bs-placement="top" title="Start ' + service.name + ' service"><svg xmlns="http://www.w3.org/2000/svg" class="icon icon-tabler icon-tabler-player-play" width="24" height="24" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" fill="none" stroke-linecap="round" stroke-linejoin="round"><path stroke="none" d="M0 0h24v24H0z" fill="none"/><path d="M7 4v16l13 -8z" /></svg></a>';
                } else if (status === 'NotStarted') {
                    actionsHtml = '<a href="/service/start/' + service.real_name + '" data-bs-toggle="tooltip" data-bs-placement="top" title="Start ' + service.name + ' service"><svg xmlns="http://www.w3.org/2000/svg" class="icon icon-tabler icon-tabler-player-play" width="24" height="24" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" fill="none" stroke-linecap="round" stroke-linejoin="round"><path stroke="none" d="M0 0h24v24H0z" fill="none"/><path d="M7 4v16l13 -8z" /></svg></a>';
                } else {
                    actionsHtml = '<a href="/service/restart/' + service.real_name + '" data-bs-toggle="tooltip" data-bs-placement="top" title="Restart ' + service.name + ' service"><svg xmlns="http://www.w3.org/2000/svg" class="icon icon-tabler icon-tabler-rotate-clockwise-2" width="24" height="24" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" fill="none" stroke-linecap="round" stroke-linejoin="round"><path stroke="none" d="M0 0h24v24H0z" fill="none"/><path d="M9 4.55a8 8 0 0 1 6 14.9m0 -4.45v5h5" /><path d="M5.63 7.16l0 .01" /><path d="M4.06 11l0 .01" /><path d="M4.63 15.1l0 .01" /><path d="M7.16 18.37l0 .01" /><path d="M11 19.94l0 .01" /></svg></a>' +
                        '<a href="/service/stop/' + service.real_name + '" data-bs-toggle="tooltip" data-bs-placement="top" title="Stop ' + service.name + ' service"><svg xmlns="http://www.w3.org/2000/svg" class="icon icon-tabler icon-tabler-player-stop" width="24" height="24" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" fill="none" stroke-linecap="round" stroke-linejoin="round"><path stroke="none" d="M0 0h24v24H0z" fill="none"/><path d="M5 5m0 2a2 2 0 0 1 2 -2h10a2 2 0 0 1 2 2v10a2 2 0 0 1 -2 2h-10a2 2 0 0 1 -2 -2z" /></svg></a>';
                }

                // Define CSS class and tooltip attributes based on status
                var statusClass = '';
                var tooltipAttributes = '';
                if (status === 'Active') {
                    statusClass = 'success';
                } else if (status === 'Inactive') {
                    statusClass = 'danger';
                } else if (status === 'NotStarted') {
                    statusClass = 'primary';
                    status = 'Not started';
                    tooltipAttributes = 'data-bs-toggle="tooltip" data-bs-placement="right" title="Service will start automatically when user/domains are created"';

                } else {
                    statusClass = 'warning';
                }

                var rowHtml = '<tr'+ tooltipAttributes +'><td>' + displayName + '</td><td><span class="badge bg-' + statusClass + ' me-1"></span>' + status + '</td><td>' + actionsHtml + '</td></tr>';

                table += rowHtml;
            });

            table += '</tbody></table>';

            // Replace the content of a div with the table
            $('#service-table').html(table);
        },
        error: function () {
            // Handle any errors here
            console.log('Error fetching service status data.');
        }
    });
}

fetchServices();
setInterval(fetchServices, 5000); 

        });



    function updateUserActivityTable() {
        $.ajax({
            url: '/combined_activity_logs',
            type: 'GET',
            dataType: 'json',
            success: function (data) {
                // Clear existing table rows
                $('#activity-table tbody').empty();



            if (data.combined_logs.length > 0) {
                // If there is data, hide the placeholder
                $('#shouldbehidden').hide();
                
                // Iterate over the logs and update the table
                data.combined_logs.forEach(function (log) {
                    // Parse the log entry
                    var parts = log.split(' ');

                    // Extract date
                    var date = parts.slice(0, 3).join(' ');

                    // Extract IP
                    var ip = parts[3];

                    // Extract userAndActivity
                    var userAndActivity = parts.slice(4).join(' ');

                    // Extract username based on role
                    var usernameMatch;
                    if (parts[4] === 'Administrator') {
                        // If the role is Administrator, username is the 4th word
                        usernameMatch = parts[5].match(/(\w+)/);
                    } else {
                        // If the role is User, username is the word after User
                        usernameMatch = userAndActivity.match(/User (\w+)/);
                    }

                    var username = usernameMatch ? usernameMatch[1] : '';

                    var formattedDate = moment(date, 'YYYY-MM-DD HH:mm:ss');
                    var now = moment();

                    var diffMinutes = Math.abs(now.diff(formattedDate, 'minutes'));
                    var isOnline = diffMinutes <= 90;

                    // Construct the avatar content based on online status and user role
                    var avatarContent;
                    if (parts[4] === 'Administrator') {
                        avatarContent = '<svg xmlns="http://www.w3.org/2000/svg" class="icon icon-tabler icon-tabler-crown" width="24" height="24" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" fill="none" stroke-linecap="round" stroke-linejoin="round"><path stroke="none" d="M0 0h24v24H0z" fill="none"></path><path d="M12 6l4 6l5 -4l-2 10h-14l-2 -10l5 4z"></path></svg>';
                    } else {
                        avatarContent = username[0].toUpperCase();
                    }

                    // Construct the avatar class based on online status
                    var avatarClass = isOnline ? '<span class="badge bg-primary"></span>' : '';
                    var displayUsernameClass = (parts[4] === 'Administrator') ? 'text-teal bg-dark' : '';
                    var displayUsername = (parts[4] === 'Administrator') ?
                        '<span class="avatar ' + displayUsernameClass + '">' + avatarContent + '</span>' :
                        '<a style="text-decoration:none;" href="/users/' + username + '#nav-activity"><span class="avatar ' + displayUsernameClass + '">' + avatarContent + '</span></a>';

                    // Build the row in the specified format
                    var row = '<div class="row">' +
                        '<div class="col-auto">' + displayUsername + '</div>' +
                        '<div class="col"><div class="text-truncate">' + userAndActivity.replace(/user (\w+)/i, 'User <strong>$1</strong>') + '</div><div class="text-secondary">' + formattedDate.format('D.M.Y H:mm:ss') + '</div></div>' +
                        '<div class="col-auto align-self-center">' + avatarClass + '</div></div>';

                    $('#activity-table .divide-y').append(row);
                });
            } else {
                // If there is no data, show the placeholder
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
            url: '/json/disk-usage',
            type: 'GET',
            dataType: 'json',
            success: function(data) {
                updateHomeUsage(data);
            },
            error: function(error) {
                console.log(error);
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
    $.get("/json/ram-usage", function(data) {
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
    fetch('/get_server_load')
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

