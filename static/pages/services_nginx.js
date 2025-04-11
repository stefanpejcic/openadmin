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
                        headers: {
                            'Content-Type': 'application/json',
                            'X-CSRFToken': $('meta[name="csrf-token"]').attr('content')
                        },
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
    function loadFileContent(filePath, textareaId, callback) {
        $.ajax({
            url: '/services/nginx/conf',
            type: 'GET',
            data: { file_path: filePath },
            success: function(response) {
                $(textareaId).val(response.config);
                if (textareaId === '#pre-landing' && callback) {
                    callback();
                }
            },
            error: function(xhr) {
                alert('Failed to load file content: ' + xhr.responseJSON.error);
            }
        });
    }

    // SAVE FILE
function saveFileContent(filePath, content) {
    $.ajax({
        url: '/services/nginx/conf?file_path=' + encodeURIComponent(filePath),
        type: 'POST',
        contentType: 'application/json',
        headers: {
            'X-CSRFToken': $('meta[name="csrf-token"]').attr('content')
        },
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
                loadFileContent('/etc/openpanel/nginx/nginx.conf', '#pre-nginxconf');
                break;
        case '#tabs-landing':
            loadFileContent('/etc/openpanel/nginx/default_page.html', '#pre-landing', updateLandingPreview);
            const preLanding = document.getElementById('pre-landing');
            const previewHtmlDefault = document.getElementById('preview_html_default');

            function updateLandingPreview() {
                const content = preLanding.value;
                previewHtmlDefault.contentWindow.document.open();
                previewHtmlDefault.contentWindow.document.write(content);
                previewHtmlDefault.contentWindow.document.close();
            }

            preLanding.addEventListener('input', updateLandingPreview);
            updateLandingPreview();
            break;

        case '#tabs-suspended_user':
            loadFileContent('/etc/openpanel/nginx/suspended_user.html', '#pre-suspended_user', updateSuspendedUserPreview);
            const preSuspendedUser = document.getElementById('pre-suspended_user');
            const previewHtmlSuspendedUser = document.getElementById('preview_html_suspended_user');

            function updateSuspendedUserPreview() {
                const content = preSuspendedUser.value;
                previewHtmlSuspendedUser.contentWindow.document.open();
                previewHtmlSuspendedUser.contentWindow.document.write(content);
                previewHtmlSuspendedUser.contentWindow.document.close();
            }

            preSuspendedUser.addEventListener('input', updateSuspendedUserPreview);
            updateSuspendedUserPreview();
            break;

        case '#tabs-suspended_website':
            loadFileContent('/etc/openpanel/nginx/suspended_website.html', '#pre-suspended_website', updateSuspendedWebsitePreview);
            const preSuspendedWebsite = document.getElementById('pre-suspended_website');
            const previewHtmlSuspendedWebsite = document.getElementById('preview_html_suspended_website');

            function updateSuspendedWebsitePreview() {
                const content = preSuspendedWebsite.value;
                previewHtmlSuspendedWebsite.contentWindow.document.open();
                previewHtmlSuspendedWebsite.contentWindow.document.write(content);
                previewHtmlSuspendedWebsite.contentWindow.document.close();
            }

            preSuspendedWebsite.addEventListener('input', updateSuspendedWebsitePreview);
            updateSuspendedWebsitePreview();
            break;
            case '#tabs-default':
                loadFileContent('/etc/openpanel/nginx/vhosts/default.conf', '#pre-default');
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
                loadFileContent('/etc/openpanel/nginx/nginx.conf', '#pre-nginxconf');
                break;
        }
    }



    // HASH OR DEFAULT
    var hash = window.location.hash;
    if (hash) {
        loadTabFromHash(hash);
        $('a[href="' + hash + '"]').tab('show');
    } else {
        loadFileContent('/etc/openpanel/nginx/nginx.conf', '#pre-nginxconf'); // Default tab
    }

    // ON TABS
    $('a[data-bs-toggle="tab"]').on('shown.bs.tab', function (e) {
        var target = $(e.target).attr("href"); // activated tab
        loadTabFromHash(target);
    });

    // ON SAVE BTNS
    $('#save_nginxconf').on('click', function() {
        saveFileContent('/etc/openpanel/nginx/nginx.conf', $('#pre-nginxconf').val());
    });
    $('#save_landing').on('click', function() {
        saveFileContent('/etc/openpanel/nginx/default_page.html', $('#pre-landing').val());
    });
    $('#save_default').on('click', function() {
        saveFileContent('/etc/openpanel/nginx/vhosts/default.conf', $('#pre-default').val());
    });
    $('#save_suspended_user').on('click', function() {
        saveFileContent('/etc/openpanel/nginx/suspended_user.html', $('#pre-suspended_user').val());
    });
    $('#save_suspended_website').on('click', function() {
        saveFileContent('/etc/openpanel/nginx/suspended_website.html', $('#pre-suspended_website').val());
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
