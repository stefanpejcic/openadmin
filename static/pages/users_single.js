// CONTENT FOR SINGLE USER PAGE
document.addEventListener('DOMContentLoaded', function() {
    var hash = window.location.hash;
    if (hash && document.querySelector('nav-' + hash)) {
        document.querySelector('nav-' + hash).click();
    }
});



// GENERATE RANDOM USERNAME AND PASSWORD

function generateRandomUsername(length) {
    const charset = "abcdefghijklmnopqrstuvwxyz0123456789";
    let result = "";
    for (let i = 0; i < length; i++) {
        const randomIndex = Math.floor(Math.random() * charset.length);
        result += charset.charAt(randomIndex);
    }
    return result;
}

function generateRandomStrongPassword(length) {
    const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*()-_=+";
    let result = "";
    for (let i = 0; i < length; i++) {
        const randomIndex = Math.floor(Math.random() * charset.length);
        result += charset.charAt(randomIndex);
    }
    return result;
}




$(document).ready(function() {
    var pathSegments = window.location.pathname.split('/');
    var containerName = pathSegments.pop();

    if (containerName.includes('_')) {
        var parts = containerName.split('_');
        containerName = parts.pop();
    }

    function formatMemorySize(bytes) {
        if (bytes >= 1e9) { // If it's over 1 GB
            return (bytes / 1e9).toFixed(2) + ' GB';
        } else {
            return (bytes / 1e6).toFixed(2) + ' MB';
        }
    }

    function formatNanoCpus(nanoCpus) {
        var cpuCores = nanoCpus / 1e9;
        var cpuCoresText = (cpuCores % 1 === 0) ? cpuCores.toFixed(0) + (cpuCores === 1 ? ' core' : ' cores') : cpuCores.toFixed(2) + ' cores';
        return cpuCoresText;
    }


    function formatCreatedDate(created) {
        const date = new Date(created);
        const day = ('0' + date.getDate()).slice(-2);
        const month = ('0' + (date.getMonth() + 1)).slice(-2);
        const year = date.getFullYear();
        const hours = ('0' + date.getHours()).slice(-2);
        const minutes = ('0' + date.getMinutes()).slice(-2);
        const seconds = ('0' + date.getSeconds()).slice(-2);

        const formattedDate = `${day}.${month}.${year} ${hours}:${minutes}:${seconds}`;

        return formattedDate;
    }

    $.get('/client/container/' + containerName, function(data) {
        if (data.error) {
            // Handle error
            $('#container-data').html('Error: ' + data.error);
        } else {
            const exposedPorts = Object.keys(data[0].Config.ExposedPorts).map(port => port.replace('/tcp', ''));
            $('#env').text("Env: " + data[0].Config.Env.join(", "));
            $('#exposedPorts').text("" + exposedPorts.join(", "));
            $('#docker-context').text("" + data[0].Config.Hostname);
            $('#docker-hostname').text("" + data[0].Config.Hostname);
            $('#image').text("" + data[0].Config.Image);
            $('#id').text("" + data[0].Id);
            const labels = data[0].Config.Labels;
            const name = labels['org.opencontainers.image.ref.name'];
            const version = labels['org.opencontainers.image.version'];
            const ubuntuIcon = '<svg xmlns="http://www.w3.org/2000/svg" class="icon icon-tabler icon-tabler-brand-ubuntu" width="24" height="24" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" fill="none" stroke-linecap="round" stroke-linejoin="round"><path stroke="none" d="M0 0h24v24H0z" fill="none"/><path d="M12 5m-2 0a2 2 0 1 0 4 0a2 2 0 1 0 -4 0" /><path d="M17.723 7.41a7.992 7.992 0 0 0 -3.74 -2.162m-3.971 0a7.993 7.993 0 0 0 -3.789 2.216m-1.881 3.215a8 8 0 0 0 -.342 2.32c0 .738 .1 1.453 .287 2.132m1.96 3.428a7.993 7.993 0 0 0 3.759 2.19m4 0a7.993 7.993 0 0 0 3.747 -2.186m1.962 -3.43a8.008 8.008 0 0 0 .287 -2.131c0 -.764 -.107 -1.503 -.307 -2.203" /><path d="M5 17m-2 0a2 2 0 1 0 4 0a2 2 0 1 0 -4 0" /><path d="M19 17m-2 0a2 2 0 1 0 4 0a2 2 0 1 0 -4 0" /></svg>';
            const debianIcon = '<svg xmlns="http://www.w3.org/2000/svg" class="icon icon-tabler icon-tabler-brand-debian" width="24" height="24" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" fill="none" stroke-linecap="round" stroke-linejoin="round"><path stroke="none" d="M0 0h24v24H0z" fill="none" /><path d="M12 17c-2.397 -.943 -4 -3.153 -4 -5.635c0 -2.19 1.039 -3.14 1.604 -3.595c2.646 -2.133 6.396 -.27 6.396 3.23c0 2.5 -2.905 2.121 -3.5 1.5c-.595 -.621 -1 -1.5 -.5 -2.5" /><path d="M12 12m-9 0a9 9 0 1 0 18 0a9 9 0 1 0 -18 0" /></svg>';


            if (name === 'ubuntu') {
                $('#labels').html(ubuntuIcon + " " + name + " " + version);
            } else if (name === 'debian') {
                $('#labels').html(debianIcon + " " + name + " " + version);
            } else {
                $('#labels').text(name + " " + version);
            }

            //$('#labels').text("Labels: " + JSON.stringify(data[0].Config.Labels));
            //$('#created').text("Created: " + data[0].Created);
            $('#created').text("" + formatCreatedDate(data[0].Created));
            $('#memory').text("" + formatMemorySize(data[0].HostConfig.Memory));
            //$('#nanoCpus').text("" + data[0].HostConfig.NanoCpus);
            //$('#nanoCpus').text("" + formatNanoCpus(data[0].HostConfig.NanoCpus));
            $('#nanoCpus').text("" + formatNanoCpus(data[0].HostConfig.NanoCpus));
            $('#mounts').text("" + JSON.stringify(data[0].Mounts));
            const firstNetwork = Object.values(data[0].NetworkSettings.Networks)[0];

            const ipAddress = firstNetwork.IPAddress;
            const macAddress = firstNetwork.MacAddress;

            $('#ipAddress').text("" + ipAddress);
            $('#macAddress').text("" + macAddress);

            $('#ipAddress2').text("" + ipAddress);
            var ports = data[0].NetworkSettings.Ports;
            var portsContainer = $('#ports-container');
            for (var port in ports) {
                if (ports.hasOwnProperty(port)) {
                    var containerIP = data[0].NetworkSettings.IPAddress;
                    var hostIP = ports[port][0].HostIp;
                    var hostPort = ports[port][0].HostPort;
                    port = port.replace("/tcp", "");

                    var portIconMap = {
                        '21': '<i class="bi bi-folder" data-bs-toggle="tooltip" data-placement="left" title="FTP"></i>',
                        '22': '<i class="bi bi-terminal" data-bs-toggle="tooltip" data-placement="left" title="SSH"></i>',
                        '3306': '<i class="bi bi-database" data-bs-toggle="tooltip" data-placement="left" title="MySQL"></i>',
                        '8080': '<i class="bi bi-database-gear" data-bs-toggle="tooltip" data-placement="left" title="phpMyAdmin"></i>',
                        '80': '<i class="bi bi-window-fullscreen" data-bs-toggle="tooltip" data-placement="left" title="Apache / Nginx"></i>',
                        '7681': '<i class="bi bi-terminal-split" data-bs-toggle="tooltip" data-placement="left" title="Web Terminal"></i>'
                    };

                    var portIcon = portIconMap[port] || '<i class="bi bi-question"></i>';
                    var listItem = $('<li>').addClass('people-item');
                    var avatar = $('<div style="font-size: x-large;">').addClass('avatar');
                    avatar.append(portIcon);
                    var peopleBody = $('<div>').addClass('people-body');
                    peopleBody.append($('<h6>').append($('<a>').attr('href', '/security/firewall?search=' + hostPort).html(':' + hostPort)));
                    listItem.append(avatar).append(peopleBody);
                    portsContainer.append(listItem);
                }
            }
            $('#stateStatus').text("" + data[0].State.Status);

            var status = data[0].State.Status;
            var statusIndicator = $('#docker_color_status_indicator');
            var statusColorTextdocker = $('#statusColorTextdocker');

            statusIndicator.removeClass("status-primary status-green status-orange status-red");
            statusColorTextdocker.removeClass("text-primary");

            if (status === "running" || status === "active") {
                statusIndicator.addClass("status-green");
                statusColorTextdocker.addClass("text-green");
            } else if (status === "not running" || status === "paused") {
                statusIndicator.addClass("status-orange");
                statusColorTextdocker.addClass("text-orange");
            } else if (status === "failed" || status === "exited" || status === "restarting") {
                statusIndicator.addClass("status-red");
                statusColorTextdocker.addClass("text-red");
            }

        }
    });





            $.ajax({
                url: `/get_custom_message_for_user/${containerName}`,
                type: 'GET',
                success: function(response) {
                    if (response.custom_message) {
                        $('#custom_message_for_this_user').val(response.custom_message);
                    } else {
                        $('#custom_message_for_this_user').val('');
                    }
                },
                error: function(xhr) {
                    // Check if the status code is 404
                    if (xhr.status === 404) {
                        // 404 when no user!
                        const response = xhr.responseJSON;
                        $('#custom_message_for_this_user').val('');
                    } else {
                        // For other errors, show the error message
                        showMessageModal('Error', xhr.responseJSON.error || 'An error occurred while retrieving the custom message.');
                    }
                }
            });











});




// SHOW USER WEBSITES
document.addEventListener("DOMContentLoaded", function() {
    var cardBtns = document.querySelectorAll('.card-btn');
    var websitesLink = document.getElementById('nav-user-data-tab');

    cardBtns.forEach(function(cardBtn) {
        cardBtn.addEventListener('click', function() {
            websitesLink.click();
        });
    });
});


// SHOW USER ACTIVITY
$(document).ready(function() {
    $("#nav-activity-tab").on("click", function() {
        var username = $("#username_for_functions").text().trim();

        $.ajax({
            type: "GET",
            url: "/user_activity/" + username,
            dataType: "json",
            success: function(data) {
                var activityTable = $("#activity-table tbody");
                activityTable.empty(); // Clear previous data

                var activityList = data.user_activity.split('\n');
                for (var i = 0; i < activityList.length; i++) {
                    var line = activityList[i].trim();
                    if (line !== "") {
                        var parts = line.split(' ');
                        var rawDateTime = parts[0] + ' ' + parts[1];
                        var date = formatDateString(rawDateTime);
                        var ip = parts[3];
                        var action = parts.slice(4).join(' ');
                        var userPrefix = "User " + username;
                        if (action.startsWith(userPrefix)) {
                            action = action.substring(userPrefix.length).trim();
                        }
                        if (action.startsWith('Administrator')) {
                            action = '<svg xmlns="http://www.w3.org/2000/svg" class="icon icon-tabler icon-tabler-crown" width="24" height="24" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" fill="none" stroke-linecap="round" stroke-linejoin="round"><path stroke="none" d="M0 0h24v24H0z" fill="none"/><path d="M12 6l4 6l5 -4l-2 10h-14l-2 -10l5 4z" /></svg> ' + action;

                            if (action.includes('unsuspended')) {
                                actiontdClass = 'text-green bg-dark';
                            } else if (action.includes('suspended')) {
                                actiontdClass = 'text-red bg-dark';
                            } else if (action.includes('edit') || action.includes('changed')) {
                                actiontdClass = 'text-yellow bg-dark';
                            } else {
                                actiontdClass = 'text-teal bg-dark';
                            }

                        } else {
                            actiontdClass = '';
                        }

                        activityTable.append("<tr class='" + actiontdClass + "'><td data-bs-toggle='tooltip' data-bs-placement='top' title='" + rawDateTime + "' class='text-nowrap text-secondary'>" + date + "</td><td>" + action + "</td><td data-bs-toggle='tooltip' data-bs-placement='top' title='Check in AbuseIPDB' class='text-nowrap text-secondary'><a href='https://www.abuseipdb.com/check/" + ip + "' target='_blank'>" + ip + "</a></td></tr>");
                    }
                }
            },
            error: function(error) {
                console.error("Error fetching user activity:", error);
            }
        });
    });



    // Check if the URL contains '#nav-activity'
    if (window.location.hash === '#nav-activity') {
        $("#nav-activity-tab").click();
    }


});

function formatDateString(rawDate) {
    var dateObj = new Date(rawDate);
    var day = dateObj.getDate();
    var month = dateObj.toLocaleString('en-US', {
        month: 'long'
    });
    var year = dateObj.getFullYear();
    var hours = dateObj.getHours();
    var minutes = dateObj.getMinutes();
    var seconds = dateObj.getSeconds();

    day = day < 10 ? '0' + day : day;
    hours = hours < 10 ? '0' + hours : hours;
    minutes = minutes < 10 ? '0' + minutes : minutes;
    seconds = seconds < 10 ? '0' + seconds : seconds;

    var currentDate = new Date();
    var currentYear = currentDate.getFullYear();

    if (year !== currentYear) {
        return day + '.' + month + '.' + year + ' ' + hours + ':' + minutes;
    } else {
        return day + '.' + month + ' ' + hours + ':' + minutes;
    }
}




// SHOW CPU USAGE


$('#nav-resource-tab').on('click', function() {
    var username = $("#username_for_functions").text().trim();
    $.ajax({
        type: 'POST',
        headers: {
            'X-CSRFToken': $('meta[name="csrf-token"]').attr('content')
        },
        url: '/get_user_stats',
        data: {
            username: username
        },
        success: function(data) {
            data.forEach(function(item) {
                item.timestamp = new Date(item.timestamp.replace(/(\d{4})-(\d{2})-(\d{2})-(\d{2})-(\d{2})-(\d{2})/, '$2/$3/$1 $4:$5'));
            });

            data.sort((a, b) => a.timestamp - b.timestamp);

            var tableHTML = '<table class="table table-striped table-hover">';
            tableHTML += '<thead><tr><th><i class="bi bi-calendar-month"></i> Date</th><th><i class="bi bi-cpu"></i> CPU %</th><th><i class="bi bi-memory"></i> Memory %</th><th><i class="bi bi-ethernet"></i> Network I/O</th><th><i class="bi bi-device-ssd"></i> Block I/O</th></tr></thead>';
            tableHTML += '<tbody>';

            for (var i = 0; i < data.length; i++) {
                tableHTML += '<tr>';
                tableHTML += '<td class="text-nowrap text-secondary">' + formatDate(data[i].timestamp) + '</td>';
                tableHTML += '<td>' + data[i].cpu_percent + '</td>';
                tableHTML += '<td>' + data[i].mem_percent + '</td>';
                tableHTML += '<td>' + data[i].net_io + '</td>';
                tableHTML += '<td>' + data[i].block_io + '</td>';
                tableHTML += '</tr>';
            }
            tableHTML += '</tbody></table>';
            $('#resource-usage-data').html(tableHTML);

            var rowCount = data.length;
            $('#info').html('Total: ' + rowCount + ' <a data-bs-toggle="collapse" style="display: inline;" class="nav-link collapsed" href="#stats_info" role="button" aria-expanded="false" aria-controls="Factor"><i class="bi bi-question-circle"></i></a>');

            // Visualization
            var trackingHTML = '';
            for (var i = 0; i < data.length; i++) {
                var cpuPercent = data[i].cpu_percent;
                var memPercent = data[i].mem_percent;
                var tooltip = formatDate(data[i].timestamp);
                var bgClass = '';

                if (cpuPercent < 1 && memPercent < 1) {
                    bgClass = '';
                } else if (cpuPercent > 75 || memPercent > 75) {
                    bgClass = 'bg-danger';
                } else if (cpuPercent > 35 || memPercent > 50) {
                    bgClass = 'bg-warning';
                } else {
                    bgClass = 'bg-success';
                }

                var popoverContent = 'CPU: ' + cpuPercent + '%<br>Memory: ' + memPercent + '%';

                trackingHTML += '<div class="tracking-block ' + bgClass + '" data-bs-toggle="popover" data-bs-trigger="hover" aria-label="' + tooltip + '" title="' + tooltip + '" data-bs-content="' + popoverContent + '"></div>';
            }
            $('.tracking').html(trackingHTML);
            $('[data-bs-toggle="popover"]').popover({ html: true });
        },
        error: function() {
            $('#info').text('No data yet');
        }
    });
});







// SHOW USER SERVICES

function loadServices() {
    var containerName = $("#username_for_functions").text().trim();
    $.ajax({
        url: '/list_services?container_name=' + containerName,
        type: 'GET',
        success: function(data) {
            $('#services-status').html('<table class="table table-striped"><thead><tr><th>Status</th><th>Service Name</th><th>Actions</th></tr></thead><tbody>' +
                data.services.map(function(service) {
                    var status = service.status === 'ON' ? '<span  class="d-inline-flex p-2 border" style="color:green;">ON&nbsp;</span>' : '<span class="d-inline-flex p-2 border" style="color:red;">OFF</span>';
                    var serviceName = service.name.trim();
                    var actions = '';
                    if (service.status === 'ON') {
                        actions = '<a href="#" onclick="manageService(\'restart\', \'' + serviceName + '\')"><i class="bi bi-arrow-clockwise"></i></a> <a href="#" onclick="manageService(\'stop\', \'' + serviceName + '\')"><i class="bi bi-stop-circle"></i></a>';
                    } else {
                        actions = '<a href="#" onclick="manageService(\'start\', \'' + serviceName + '\')"><i class="bi bi-play-circle"></i></a> <a href="#" onclick="manageService(\'restart\', \'' + serviceName + '\')"><i class="bi bi-arrow-clockwise"></i></a>';
                    }

                    return '<tr style="vertical-align: middle;"><td>' + status + '</td><td>' + serviceName + '</td><td style="font-size: x-large;">' + actions + '</td></tr>';
                }).join('') + '</tbody></table>');
        },
        error: function(error) {
            console.error('Error loading services:', error);
        }
    });
}

function manageService(action, serviceName) {
    var containerName = $("#username_for_functions").text().trim();
    $.ajax({
        url: '/service/' + action + '/' + serviceName + '?container=' + containerName,
        type: 'POST',
        headers: {
            'X-CSRFToken': $('meta[name="csrf-token"]').attr('content')
        },
        success: function(data) {
            loadServices();
        },
        error: function(error) {
            console.error('Error managing service:', error);
        }
    });
}

$('#nav-manage-tab').on('click', loadServices);




// SHOW USER BACKUPS
$(document).ready(function() {
    $("#nav-backups-tab").click(function() {
        var containerName = $("#username_for_functions").text().trim();

        $.ajax({
            url: "/backups/restore/dates/" + containerName,
            type: "GET",
            dataType: "json",
            success: function(response) {
                displayBackupInfo(response);
            },
            error: function(error) {
                console.error("Error fetching backup information:", error);
            }
        });
    });

    function displayBackupInfo(backupInfo) {
        var backupsInfoDiv = $("#backups2_info");
        var backupsDiv = $("#backups");

        backupsInfoDiv.empty();
        backupsDiv.empty();

        backupsInfoDiv.html('Total: ' + backupInfo.backups.length + ' <a data-bs-toggle="collapse" style="display: inline;" class="nav-link collapsed" href="#backups_info" role="button" aria-expanded="false" aria-controls="Factor"><i class="bi bi-question-circle"></i></a>');



        var tableHtml = "<table class='table table-striped table-hover'><thead><tr><th>Created</th><th>Contains</th><th>Status</th><th class='d-none'>Duration (s)</th></tr></thead><tbody>";

        $.each(backupInfo.backups, function(_, backup) {
            var formattedTimestamp = formatTimestamp(backup.backup_date);
            var statusHtml = getStatusHtml(backup.content.status);
            var totalExecTime = backup.content.total_exec_time;

            tableHtml += "<tr><td class='text-nowrap text-secondary'>" + formattedTimestamp + "</td><td>" + getContentIcons(backup) + "</td><td>" + statusHtml + "</td><td class='d-none'>" + totalExecTime + "</td></tr>";
        });

        tableHtml += "</tbody></table>";
        backupsDiv.html(tableHtml);

        // Initialize Bootstrap tooltips
        $('[data-bs-toggle="tooltip"]').tooltip();
    }

    function formatTimestamp(timestamp) {
        var year = timestamp.substring(0, 4);
        var month = timestamp.substring(4, 6);
        var day = timestamp.substring(6, 8);
        var hours = timestamp.substring(8, 10);
        var minutes = timestamp.substring(10, 12);

        var date = new Date(year, month - 1, day, hours, minutes);
        return formatDate(date);
    }

    function getContentIcons(backup) {
        const iconMap = {
            'FILES': 'bi-file-earmark', // Bootstrap icon for files
            'ENTRYPOINT': 'bi-play', // Bootstrap icon for entrypoint
            'WEBSERVER_CONF': 'bi-server', // Bootstrap icon for web server
            'MYSQL_CONF': 'bi-database', // Bootstrap icon for MySQL
            'TIMEZONE': 'bi-clock', // Bootstrap icon for timezone
            'PHP_VERSIONS': 'bi-code-slash', // Bootstrap icon for PHP versions
            'CRONTAB': 'bi-calendar', // Bootstrap icon for crontab
            'MYSQL_DATA': 'bi-database', // Bootstrap icon for MySQL data
            'USER_DATA': 'bi-people', // Bootstrap icon for user data
            'CORE_USERS': 'bi-shield-lock', // Bootstrap icon for core users
            'STATS_USERS': 'bi-graph-up', // Bootstrap icon for stats users
            'APACHE_SSL_CONF': 'bi-lock', // Bootstrap icon for SSL configuration
            'DOMAIN_ACCESS_REPORTS': 'bi-file-earmark-text', // Bootstrap icon for domain access reports
            'SSH_PASS': 'bi-key', // Bootstrap icon for SSH password
            'IMAGE': 'bi-ubuntu' // Bootstrap icon for images
        };

        var iconsHtml = '';
        const containsArray = backup.content.contains.split(',');
        containsArray.forEach(item => {
            if (iconMap[item]) {
                const icon = `<i class="bi ${iconMap[item]} me-2" title="${item}" data-bs-toggle="tooltip"></i>`;
                iconsHtml += icon;
            }
        });

        if (iconsHtml.length === 0) {
            iconsHtml = '-';
        }

        return iconsHtml;
    }

    function getStatusHtml(status) {
        let iconClass;
        let textClass;

        switch (status) {
            case 'Completed':
                iconClass = 'bi-check-circle';
                textClass = 'text-success';
                break;
            case 'Partial':
                iconClass = 'bi-exclamation-circle';
                textClass = 'text-warning';
                break;
            case 'In Progress':
                iconClass = 'bi-spinner';
                textClass = 'text-primary';
                break;
            default:
                iconClass = 'bi-question-circle';
                textClass = 'text-danger';
                status = 'Unknown';
        }

        return `<i class="bi ${iconClass} me-2 ${textClass}" title="${status}" data-bs-toggle="tooltip"></i>`;
    }
    
    function formatDate(date) {
        return date.toLocaleString();
    }
});





// SHOW USER RESOURCE USAGE

function formatDate(date) {
    var day = ('0' + date.getDate()).slice(-2);
    var month = date.toLocaleString('en-US', {
        month: 'long'
    });
    var year = date.getFullYear();
    var currentYear = new Date().getFullYear();
    var hours = ('0' + date.getHours()).slice(-2);
    var minutes = ('0' + date.getMinutes()).slice(-2);

    if (year === currentYear) {
        return day + '.' + month.slice(0, 3) + ' ' + hours + ':' + minutes;
    } else {
        return day + '.' + month.slice(0, 3) + ' ' + year + ' ' + hours + ':' + minutes;
    }
}



// EDIT USER DNS FOR DOMAIN

$(document).ready(function() {
    var currentDomain;

    $('.edit_caddy_link').click(function(e) {
        e.preventDefault();
        currentDomain = $(this).data('domain');
        $.get(`/caddy-vhosts/${currentDomain}`, function(data) {
            $('#caddyContent').val(data.caddy_content);
            $('#domain_name_caddy').text(`Caddy vHosts for ${currentDomain}`);
            $('#caddyOffcanvas').addClass('show');
        });
    });

    $('#saveCaddy').click(function() {
        var newContent = $('#caddyContent').val();
        $.ajax({
            type: 'POST',
            headers: {
                'X-CSRFToken': $('meta[name="csrf-token"]').attr('content')
            },
            url: `/save-vhosts/${currentDomain}`,
            contentType: 'application/json;charset=UTF-8',
            data: JSON.stringify({
                'new_content': newContent
            }),
            success: function(response) {
                alert('Caddy configuration saved successfully!');
            },
            error: function(error) {
                alert('Error saving Caddy configuration: ' + error.responseText);
            }
        });
    });


    $('#closeOffcanvasBtn').click(function() {
        $('#caddyOffcanvas').removeClass('show');
    });


    $(document).on('click', function(e) {
        if (!$(e.target).closest('.edit_caddy_link').length && !$(e.target).closest('#caddyOffcanvas').length) {
            $('#caddyOffcanvas').removeClass('show');
        }
    });
});


// SHOW USER DOMAIN CADDY ACCESS LOG

$(document).ready(function() {
    var currentDomain;

    $('.view_log_link').click(function(e) {
        e.preventDefault();
        currentDomain = $(this).data('domain');
        $.get(`/caddy-logs/${currentDomain}`, function(data) {
            $('#logContent').text(data.caddy_content).scrollTop($('#logContent')[0].scrollHeight);
            $('#log_domain_name').text(`Access Log for ${currentDomain}`);
            $('#logOffcanvas').addClass('show');
        });
    });

    $('#closeOffcanvasBtn').click(function() {
        $('#logOffcanvas').removeClass('show');
    });

    $(document).on('click', function(e) {
        if (!$(e.target).closest('.view_log_link').length && !$(e.target).closest('#logOffcanvas').length) {
            $('#logOffcanvas').removeClass('show');
        }
    });
});


// EDIT USER DNS ZONE
$(document).ready(function() {
    var currentDomain;

    $('.edit_dns_link').click(function(e) {
        e.preventDefault();

        currentDomain = $(this).data('domain');

        $.get(`/dns-bind/${currentDomain}`, function(data) {
            $('#dnsContent').val(data.bind_content);
            $('#domain_name').text(`Edit DNS for ${currentDomain}`);
            $('#dnsOffcanvas').addClass('show');
        });
    });

    $('#saveDns').click(function() {
        var newContent = $('#dnsContent').val();

        $.ajax({
            type: 'POST',
            headers: {
                'X-CSRFToken': $('meta[name="csrf-token"]').attr('content')
            },
            url: `/save-dns/${currentDomain}`,
            contentType: 'application/json;charset=UTF-8',
            data: JSON.stringify({
                'new_content': newContent
            }),
            success: function(response) {
                alert('DNS content saved successfully!');
            },
            error: function(error) {
                alert('Error saving DNS content: ' + error.responseText);
            }
        });
    });


    $('#closeOffcanvasBtn').click(function() {
        $('#dnsOffcanvas').removeClass('show');
    });


    $(document).on('click', function(e) {
        if (!$(e.target).closest('.edit_dns_link').length && !$(e.target).closest('#dnsOffcanvas').length) {
            $('#dnsOffcanvas').removeClass('show');
        }
    });
});


// DELETE USER ACCOUNT

document.getElementById("deleteButton").addEventListener("click", function(event) {
    event.preventDefault();
    document.getElementById("deleteConfirmation").value = "";
    document.getElementById("confirmDelete").style.display = "none";
});

document.getElementById("deleteConfirmation").addEventListener("input", function() {
    var confirmationInput = this.value.trim().toUpperCase();
    document.getElementById("confirmDelete").style.display = (confirmationInput === "DELETE") ? "block" : "none";
});

document.getElementById("confirmDelete").addEventListener("click", function() {

    var username = document.getElementById("deleteButton").getAttribute("href").split('/').pop();
    var deleteModalxClose = document.getElementById("deleteModalxClose");
    var confirmDeleteUserModal = document.getElementById("confirmDeleteUserModal");
    var modalFooter = document.getElementById("confirmDeleteUserModal").getElementsByClassName("modal-footer")[0];

    confirmDeleteUserModal.setAttribute("data-bs-backdrop", "static");
    confirmDeleteUserModal.setAttribute("data-bs-keyboard", "false");
    modalFooter.style.display = "none";
    deleteModalxClose.style.display = "none";
    var modalBody = document.getElementById("confirmDeleteUserModal").getElementsByClassName("modal-body")[0];
    modalBody.innerHTML = `
    <div class="text-center">
      <div class="text-secondary mb-3">Deleting user, please wait..</div>
      <div class="progress progress-sm">
        <div class="progress-bar progress-bar-indeterminate"></div>
      </div>
    </div>
  `;

    fetch(`/user/delete/${username}`, {
            method: "GET",
        })
        .then(response => response.json())
        .then(data => {
            if (data.message.includes("successfully")) {
                window.location.href = "/users";
            } else {
                console.error("Error terminating user:", data.message);
            }
        })
        .catch(error => {
            console.error("Error sending request:", error);
        });

    var modal = new bootstrap.Modal(document.getElementById("confirmDeleteUserModal"));
    modal.hide();
});




// SUSPEND USER ACCOUNT

var suspendButton = document.getElementById("suspendButton");
if (suspendButton) {
    suspendButton.addEventListener("click", function(event) {
        event.preventDefault();
    });


    var confirmSuspend = document.getElementById("confirmSuspend");
    if (confirmSuspend) {
        confirmSuspend.addEventListener("click", function() {
            var username = document.getElementById("suspendButton").getAttribute("href").split('/').pop();
            var suspendModalxClose = document.getElementById("suspendModalxClose");
            var confirmSuspendUserModal = document.getElementById("confirmSuspendUserModal");
            var modalSuspendFooter = document.getElementById("confirmSuspendUserModal").getElementsByClassName("modal-footer")[0];

            confirmSuspendUserModal.setAttribute("data-bs-backdrop", "static");
            confirmSuspendUserModal.setAttribute("data-bs-keyboard", "false");
            modalSuspendFooter.style.display = "none";
            suspendModalxClose.style.display = "none";
            var modalBody = document.getElementById("confirmSuspendUserModal").getElementsByClassName("modal-body")[0];
            modalBody.innerHTML = `
    <div class="text-center">
      <div class="text-secondary mb-3">Suspending user, please wait..</div>
      <div class="progress progress-sm">
        <div class="progress-bar progress-bar-indeterminate"></div>
      </div>
    </div>
  `;


            fetch(`/user/suspend/${username}`, {
                    method: "GET",
                })
                .then(response => response.json())
                .then(data => {
                    if (data.message.includes("successfully")) {
                        window.location.href = "/users";
                    } else {
                        console.error("Error suspending user:", data.message);
                    }
                })
                .catch(error => {
                    console.error("Error sending request:", error);
                });

            var modal = new bootstrap.Modal(document.getElementById("confirmSuspendUserModal"));
            modal.hide();
        });

    }

}


// UNSUSPEND USER ACCOUNT

var unsuspendButton = document.getElementById("unsuspendButton");
if (unsuspendButton) {
    unsuspendButton.addEventListener("click", function() {
        var href = unsuspendButton.getAttribute("href");
        var username = href.split('_').pop();
        var url = `/${username}`;

        fetch(url, {
                method: "GET",
            })
            .then(response => response.json())
            .then(data => {
                if (data.message.includes("successfully")) {
                    window.location.reload();
                } else {
                    console.error("Error unsuspending user:", data.message);
                }
            })
            .catch(error => {
                console.error("Error sending request:", error);
            });
    });
}

// EDIT USER INFO MODAL

function loadData() {
    async function fetchData(route) {
        try {
            const response = await fetch(route);
            const data = await response.json();
            return data;
        } catch (error) {
            console.error('Error fetching data:', error);
        }
    }

    async function openModal() {
        const ipSelect = document.getElementById('ipSelect');
        ipSelect.innerHTML = '';

        const planSelectGroup = document.getElementById('planSelectGroup');
        planSelectGroup.innerHTML = '';

        const ipsData = await fetchData('/system/ips');
        const plansData = await fetchData('/plans?output=json');
        const allIPAddresses = ipsData.ip_addresses || [];
        const allPlans = plansData.plans || [];

        const planNameElement = document.getElementById('plan_name_for_functions');
        const currentUserPlanName = planNameElement ? planNameElement.textContent.trim() : '';

        allIPAddresses.forEach(ip => {
            const option = document.createElement('option');
            option.value = ip;
            option.textContent = ip;
            ipSelect.appendChild(option);
        });

        allPlans.forEach((plan, index) => {
            const planItem = document.createElement('div');
            planItem.classList.add('col-lg-6', 'mb-2');

            const label = document.createElement('label');
            label.classList.add('form-selectgroup-item');

            const input = document.createElement('input');
            input.type = 'radio';
            input.name = 'plan-type';
            input.value = plan.name;
            input.classList.add('form-selectgroup-input');
            if (plan.name === currentUserPlanName) {
                input.checked = true;
            }

            const span = document.createElement('span');
            span.classList.add('form-selectgroup-label', 'd-flex', 'align-items-center', 'p-3');

            const innerSpan = document.createElement('span');
            innerSpan.classList.add('me-3');
            innerSpan.innerHTML = '<span class="form-selectgroup-check"></span>';

            const contentSpan = document.createElement('span');
            contentSpan.classList.add('form-selectgroup-label-content');

            const titleSpan = document.createElement('span');
            titleSpan.classList.add('form-selectgroup-title', 'strong', 'mb-1');
            titleSpan.textContent = plan.name;

            if (plan.name === currentUserPlanName) {
                const ribbonDiv = document.createElement('div');
                ribbonDiv.classList.add('ribbon', 'bg-primary');
                ribbonDiv.textContent = 'current';
                label.appendChild(ribbonDiv);
            }

            const descSpan = document.createElement('span');
            descSpan.classList.add('d-block', 'text-muted');
            descSpan.textContent = plan.description;

            span.appendChild(innerSpan);
            span.appendChild(contentSpan);
            label.appendChild(input);
            label.appendChild(span);
            contentSpan.appendChild(titleSpan);
            contentSpan.appendChild(descSpan);
            planItem.appendChild(label);
            planSelectGroup.appendChild(planItem);
        });

        //const modal = new bootstrap.Modal(document.getElementById('modal-edit-user'));
        //modal.show();
    }

    openModal();
}



// SHOW DISK USAGE FOR USER

function formatBytes(bytes) {
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
    if (bytes === 0) return '0 B';
    const i = parseInt(Math.floor(Math.log(bytes) / Math.log(1024)));
    return Math.round((bytes / Math.pow(1024, i)) * 100) / 100 + ' ' + sizes[i];
}

function formatBytesToBigger(bytes) {
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
    if (bytes === 0) return '0 B';
    const i = parseInt(Math.floor(Math.log(bytes) / Math.log(1024)));
    const roundedValue = i > 2 ? Math.ceil(bytes / Math.pow(1024, i)) : Math.round(bytes / Math.pow(1024, i));

    return roundedValue + ' ' + sizes[i];
}



// DELETE USER
$(document).ready(function() {
    $('.user_delete').click(function() {
        var username = $(this).data('username');

        $('#usernameToDelete').text(username);
        $('#usernameToDeletehidden').val(username);

    });
});


// PREFIL DATA, RENAME USER

$(document).ready(function() {
    $('.btn-primary').click(function() {
        var username = $(this).data('username');
        var email = $(this).data('email');
        var ip = $(this).data('ip');
        var plan_id = $(this).data('plan_id');

        $('#old_user').val(username);
        $('#new_user').val(username);
        $('#new_email').val(email);
        $('#new_ip').val(ip);
        $('#new_plan_id').val(plan_id);
    });
});

