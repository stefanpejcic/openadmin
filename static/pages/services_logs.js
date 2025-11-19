// SHOW LOG FILE CONTENT
function fetchLog() {
    const logSelect = document.getElementById('log-select');
    const logName = logSelect.value;
    const linesSelect = document.getElementById('lines-select');
    const linesValue = linesSelect.value;

    if (logName === '/settings/updates/log/' || logName === '/services/crashlogs/log/') {
        window.location.href = logName;
        return;
    }

    let url = `/services/logs/raw?log_name=${logName}`;
    if (linesValue !== 'ALL') {
        url += `&lines=${linesValue}`;
    }

    fetch(url)
        .then(response => response.json())
        .then(data => {
            const logContentDiv = document.getElementById('log-content');
            logContentDiv.textContent = data.error || (data.content.trim() ? data.content : 'The log file is empty.');
            document.getElementById('truncate-btn').classList.remove('hidden');
            document.getElementById('download-btn').classList.remove('hidden');
            document.getElementById('logs-tip').classList.add('hidden');
        })
        .catch(error => {
            console.error('Error fetching log:', error);
            document.getElementById('log-content').textContent = 'Error fetching log data.';
        });
}


function updateURLParams() {
    const logName = document.getElementById('log-select').value;
    const linesValue = document.getElementById('lines-select').value;

    const url = new URL(window.location);
    url.searchParams.set('log_name', logName);

    if (linesValue !== 'ALL') {
        url.searchParams.set('lines', linesValue);
    } else {
        url.searchParams.delete('lines');
    }

    // Update URL without reloading the page
    history.replaceState(null, '', url.toString());
}

function getURLParam(param) {
    const urlParams = new URLSearchParams(window.location.search);
    return urlParams.get(param);
}


document.addEventListener('DOMContentLoaded', function() {
    // Handle log_name
    const logNameFromURL = getURLParam('log_name');
    if (logNameFromURL) {
        const logSelect = document.getElementById('log-select');
        const options = logSelect.getElementsByTagName('option');
        for (let option of options) {
            if (option.value === logNameFromURL) {
                option.selected = true;
                break;
            }
        }
        fetchLog();
    }

    // Handle lines (optional)
    const linesFromURL = getURLParam('lines');
    if (linesFromURL) {
        const linesSelect = document.getElementById('lines-select');
        const options = linesSelect.getElementsByTagName('option');
        for (let option of options) {
            if (option.value === linesFromURL) {
                option.selected = true;
                break;
            }
        }
    }


// Update URL when selects change
document.getElementById('log-select').addEventListener('change', function() {
    fetchLog();
    updateURLParams();
});

document.getElementById('lines-select').addEventListener('change', function() {
    fetchLog();
    updateURLParams();
});



});

// DOWNLOAD LOG FILE
function downloadLog() {
    const logName = document.getElementById('log-select').value;
    const url = `/services/logs/raw?log_name=${logName}`;

    fetch(url, {
        method: 'POST',
        headers: {
            'X-CSRFToken': document.querySelector('meta[name="csrf-token"]').content
        }
    })
    .then(response => response.blob())
    .then(blob => {
        const downloadUrl = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = downloadUrl;
        a.download = `${logName}.txt`;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        URL.revokeObjectURL(downloadUrl);
    })
    .catch(error => {
        console.error('Error downloading log:', error);
        alert('Error downloading log file.');
    });
}

// EMPTY LOG CONTENT
function truncateLog() {
    const logName = document.getElementById('log-select').value;
    fetch(`/services/logs/raw?log_name=${logName}`, {
        method: 'DELETE',
        headers: {
            'X-CSRFToken': document.querySelector('meta[name="csrf-token"]').content
        }
    })
    .then(response => response.json())
    .then(data => alert(data.message));
}

document.addEventListener('DOMContentLoaded', function() {
    document.getElementById('truncate-btn').addEventListener('click', truncateLog);
});

// SHOW RANDOM TIP ON START
window.onload = function() {
    const logContent = document.getElementById('logs-tip-text');
    const messages = [
        `You can add custom log files to this viewer.`,
        `Download log files by selecting a log and clicking the 'Download' button below the content.`,
        `Empty logs by selecting a log file and clicking the 'Delete' button below the content.`,
        `Change the number of lines using the 'Lines' select. 'ALL' displays 1M lines.`
    ];
    logContent.innerHTML = messages[Math.floor(Math.random() * messages.length)];
    document.getElementById('logs-tip').classList.remove('hidden');
};
