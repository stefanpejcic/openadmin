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
                        '<a style="text-decoration:none;" href="/users/' + username + '#activity_log"><span class="avatar ' + displayUsernameClass + '">' + avatarContent + '</span></a>';

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


        // Use Ajax to get Docker context data
        $(document).ready(function() {
            $.ajax({
                url: '/json/docker-context',
                type: 'GET',
                dataType: 'json',
                success: function(data) {
                    updateDockerContextsTable(data);
                },
                error: function(error) {
                    console.log(error);
                }
            });
        });

        // Update the Bootstrap table with the received data
        function updateDockerContextsTable(data) {
            var tableBody = $('#docker-contexts-table tbody');
            tableBody.empty(); // Clear the table body

            $.each(data, function(index, context) {
                var row = '<tr>' +
                    '<td>' + context.Name + '</td>' +
                    '<!--td>' + context.Description + '</td-->' +
                    '<td>' + context.DockerEndpoint + '</td>' +
                    '<td>' + (context.Current ? 'Yes' : 'No') + '</td>' +
                    '<!--td>' + context.Error + '</td-->' +
                    '</tr>';
                tableBody.append(row);
            });
        }



    // Use Ajax to get disk usage data
    $(document).ready(function() {
        $.ajax({
            url: '/json/disk-usage',
            type: 'GET',
            dataType: 'json',
            success: function(data) {
                updateDiskUsageTable(data);
            },
            error: function(error) {
                console.log(error);
            }
        });
    });

// Update the Bootstrap table with the received data
function updateDiskUsageTable(data) {
    var tableBody = $('#disk-usage-table tbody');
    tableBody.empty(); // Clear the table body

    $.each(data, function(index, disk_info) {
        // Limit 'device' and 'mountpoint' to 50 characters
        var truncatedDevice = disk_info.device.length > 18 ? disk_info.device.substring(0, 18) + '...' : disk_info.device;
        var truncatedMountpoint = disk_info.mountpoint.length > 40 ? disk_info.mountpoint.substring(0, 40) + '...' : disk_info.mountpoint;

        // Create a table row ('<tr>') with information from the 'disk_info' object
        var row = '<tr>' +
            '<td ' + (disk_info.device.length > 18 ? 'data-bs-toggle="tooltip" data-placement="top" title="' + disk_info.device + '"' : '') + '>' + truncatedDevice + '</td>' +
            '<td ' + (disk_info.mountpoint.length > 40 ? 'data-bs-toggle="tooltip" data-placement="top" title="' + disk_info.mountpoint + '"' : '') + '><div class="progressbg"><div class="progress progressbg-progress"><div class="progress-bar bg-primary-lt" style="width: ' + disk_info.percent + '%" role="progressbar" aria-valuenow="' + disk_info.percent + '" aria-valuemin="0" aria-valuemax="100" aria-label="' + disk_info.percent + '% Used"><span class="visually-hidden">' + disk_info.percent + '% Complete</span></div></div><div class="progressbg-text">' + truncatedMountpoint + '</div></div></td>' +
            '<!--td>' + disk_info.fstype + '</td-->' +
            '<td class="text-secondary">' + formatDiskSize(disk_info.used) + '</td>' +
            '<td class="text-secondary">' + formatDiskSize(disk_info.total) + '</td>' +
            '<td class="text-secondary">' + formatDiskSize(disk_info.free) + '</td>' +
            '<td class="text-secondary ' + getColorClass(disk_info.percent) + '">' + disk_info.percent + '</td>' +
            '</tr>';


        tableBody.append(row);
    });

    // Enable Bootstrap tooltips
    $('[data-bs-toggle="tooltip"]').tooltip();
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
            return tb.toFixed(2) + ' TB'; // Display in TB with two decimal places
        } else {
            var gb = bytes / (1024 * 1024 * 1024); // Convert to GB
            return gb.toFixed(2) + ' GB'; // Display in GB with two decimal places
        }
    }



    // Function to update RAM info
    function updateRamInfo() {
        $.get("/json/ram-usage", function(data) {
            var html = data.human_readable_info.used + " / " + data.human_readable_info.total + " (" + data.human_readable_info.percent + ")";
            var percentString = data.human_readable_info.percent;
            var percent = parseInt(percentString.slice(0, -1));
            $("#human-readable-info").html(html);

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


        function getServerLoad() {
            fetch('/get_server_load')
                .then(response => response.json())
                .then(loadData => {
                    const load1min = parseFloat(loadData.load1min);
                    const load5min = parseFloat(loadData.load5min);
                    const load15min = parseFloat(loadData.load15min);
                    document.getElementById('load1min').textContent = load1min.toFixed(2);
                    document.getElementById('load5min').textContent = load5min.toFixed(2);
                    document.getElementById('load15min').textContent = load15min.toFixed(2);



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
            refreshServerLoadandRamUsage();
        };

