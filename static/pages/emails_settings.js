// GET NGINX VERSION, MODULES AND VHOSTS COUNT
                async function fetchNginxInfo() {
                    const response = await fetch('/services/nginx/info');
                    const data = await response.json();
                    
                    document.getElementById('nginx-version').innerText = data.version;
                    updateNginxStatus(data.status);
                    document.getElementById('nginx-modules').innerText = data.modules.join(', ');
                    document.getElementById('nginx-vhosts-count').innerText = data.vhosts_count;
                }

// GET NGINX STATUS
                function updateNginxStatus(status) {
                    const statusElement = document.getElementById('nginx-status');
                    statusElement.innerText = status;

                    if (status === 'active') {
                        statusElement.classList.add('text-green');
                    } else {
                        statusElement.classList.add('text-red');
                    }
                }

// GET NGINX STATUS FROM BUILTIN PAGE
                async function fetchStatus() {
                    const response = await fetch('/services/nginx/status');
                    const data = await response.json();
                    document.getElementById('nginx-active-connections').innerText = data.active_connections;
                    document.getElementById('nginx-accepts-handled-requests').innerText = data.accepts_handled_requests.join(' ');
                    document.getElementById('nginx-reading').innerText = data.reading;
                    document.getElementById('nginx-writing').innerText = data.writing;
                    document.getElementById('nginx-waiting').innerText = data.waiting;
                }

// START, STOP, RELOAD..
                async function controlNginx(action) {
                    const response = await fetch('/services/nginx/control', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ action: action })
                    });
                    const data = await response.json();
                    alert(data.message);
                }

// nginx -t
                async function validateConfig() {
                    const response = await fetch('/services/nginx/validate');
                    const data = await response.json();
                    alert(data.message);
                }

                window.onload = () => {
                    fetchNginxInfo();
                    fetchStatus();
                }





$(document).ready(function() {
    //  READ FILE
    function loadFileContent(filePath, textareaId) {
        $.ajax({
            url: '/services/mailserver/conf',
            type: 'GET',
            data: { file_path: filePath },
            success: function(response) {
                $(textareaId).val(response.config);
            },
            error: function(xhr) {
                alert('Failed to load file content: ' + xhr.responseJSON.error);
            }
        });
    }

    // SAVE FILE
    function saveFileContent(filePath, content) {
        $.ajax({
            url: '/services/mailserver/conf?file_path=' + encodeURIComponent(filePath),
            type: 'POST',
            contentType: 'application/json',
            data: JSON.stringify({ config: content }),
            success: function(response) {
                alert('Configuration updated successfully');
            },
            error: function(xhr) {
                alert('Failed to save file content: ' + xhr.responseJSON.error);
            }
        });
    }

    // LOAD CONTENT ON DIRECT LINK
    function loadTabFromHash(hash) {
        switch (hash) {
            case '#tabs-nginxconf':
                loadFileContent('/usr/local/mail/openmail/mailserver.env', '#pre-nginxconf');
                break;
            case '#tabs-default':
                loadFileContent('/usr/local/mail/openmail/compose.yml', '#pre-default');
                break;
            case '#tabs-domain':
                loadFileContent('/etc/openpanel/nginx/vhosts/domain.conf', '#pre-domain');
                break;
            case '#tabs-conf_with_modsec':
                loadFileContent('/etc/openpanel/nginx/vhosts/domain.conf_with_modsec', '#pre-conf_with_modsec');
                break;
            case '#tabs-docker_nginx_domain':
                loadFileContent('/etc/openpanel/nginx/vhosts/docker_nginx_domain.conf', '#pre-docker_nginx_domain');
                break;
            case '#tabs-docker_apache_domain':
                loadFileContent('/etc/openpanel/nginx/vhosts/docker_apache_domain.conf', '#pre-docker_apache_domain');
                break;
            case '#tabs-openpanel_proxy':
                loadFileContent('/etc/openpanel/nginx/vhosts/openpanel_proxy.conf', '#pre-openpanel_proxy');
                break;
            default:
                loadFileContent('/usr/local/mail/openmail/mailserver.env', '#pre-nginxconf');
                break;
        }
    }

    // HASH OR DEFAULT
    var hash = window.location.hash;
    if (hash) {
        loadTabFromHash(hash);
        $('a[href="' + hash + '"]').tab('show');
    } else {
        loadFileContent('/usr/local/mail/openmail/mailserver.env', '#pre-nginxconf'); // Default tab
    }

    // ON TABS
    $('a[data-bs-toggle="tab"]').on('shown.bs.tab', function (e) {
        var target = $(e.target).attr("href"); // activated tab
        loadTabFromHash(target);
    });

    // ON SAVE BTNS
    $('#save_nginxconf').on('click', function() {
        saveFileContent('/usr/local/mail/openmail/mailserver.env', $('#pre-nginxconf').val());
    });
    $('#save_default').on('click', function() {
        saveFileContent('/usr/local/mail/openmail/compsoe.yml', $('#pre-default').val());
    });
    $('#save_domain').on('click', function() {
        saveFileContent('/etc/openpanel/nginx/vhosts/domain.conf', $('#pre-domain').val());
    });
    $('#save_conf_with_modsec').on('click', function() {
        saveFileContent('/etc/openpanel/nginx/vhosts/domain.conf_with_modsec', $('#pre-conf_with_modsec').val());
    });
    $('#save_docker_nginx_domain').on('click', function() {
        saveFileContent('/etc/openpanel/nginx/vhosts/docker_nginx_domain.conf', $('#pre-docker_nginx_domain').val());
    });
    $('#save_docker_apache_domain').on('click', function() {
        saveFileContent('/etc/openpanel/nginx/vhosts/docker_apache_domain.conf', $('#pre-docker_apache_domain').val());
    });
    $('#save_openpanel_proxy').on('click', function() {
        saveFileContent('/etc/openpanel/nginx/vhosts/openpanel_proxy.conf', $('#pre-openpanel_proxy').val());
    });
});
