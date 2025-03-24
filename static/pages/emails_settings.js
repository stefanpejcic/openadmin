
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
            case '#tabs-env':
                loadFileContent('/usr/local/mail/openmail/mailserver.env', '#env_file');
                break;
            case '#tabs-compose':
                loadFileContent('/usr/local/mail/openmail/compose.yml', '#compose');
                break;
        }
    }

    // HASH OR DEFAULT
    var hash = window.location.hash;
    if (hash) {
        loadTabFromHash(hash);
        $('a[href="' + hash + '"]').tab('show');
    }

    // ON TABS
    $('a[data-bs-toggle="tab"]').on('shown.bs.tab', function (e) {
        var target = $(e.target).attr("href"); // activated tab
        loadTabFromHash(target);
    });

    // ON SAVE BTNS
    $('#save_env').on('click', function() {
        saveFileContent('/usr/local/mail/openmail/mailserver.env', $('#env_file').val());
    });
    $('#save_compose').on('click', function() {
        saveFileContent('/usr/local/mail/openmail/compose.yml', $('#compose').val());
    });
});
