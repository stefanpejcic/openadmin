// SHOW LOG FILE CONTENT
    function fetchLog() {
        const logSelect = document.getElementById('log-select');
        const logName = logSelect.options[logSelect.selectedIndex].value;
        const linesSelect = document.getElementById('lines-select');
        const linesValue = linesSelect.options[linesSelect.selectedIndex].value;

        let url = `/services/logs/raw?log_name=${logName}`;
        if (linesValue !== 'ALL') {
            url += `&lines=${linesValue}`;
        }

        fetch(url)
        .then(response => response.json())
        .then(data => {
            const logContentDiv = document.getElementById('log-content');
            if (data.error) {
                logContentDiv.textContent = data.error;
            } else {
                if (data.content.trim() === '') {
                    logContentDiv.textContent = 'The log file is empty.';
                } else {
                    logContentDiv.textContent = data.content;
                }
                document.getElementById('truncate-btn').classList.remove('d-none'); //show truncate btn
                document.getElementById('download-btn').classList.remove('d-none'); //show download btn
                document.getElementById('logs-tip').classList.add('d-none'); //hide tip
            }
        })
        .catch(error => {
            console.error('Error fetching log:', error);
            document.getElementById('log-content').textContent = 'Error fetching log data.';
        });
    }

    // Initialize event listeners and set initial selected options
    document.addEventListener('DOMContentLoaded', function() {
        const logSelect = document.getElementById('log-select');
        const linesSelect = document.getElementById('lines-select');
        
        logSelect.addEventListener('change', fetchLog);
        linesSelect.addEventListener('change', fetchLog);

        //fetchLog();
        //linesSelect.value = "1000";
    });



// DOWNLOAD LOG FILE
function downloadLog() {
    const logSelect = document.getElementById('log-select');
    const logName = logSelect.options[logSelect.selectedIndex].value;

    // Construct URL for the POST request to download log file
    const url = `/services/logs/raw?log_name=${logName}`;

    fetch(url, {
        method: 'POST',
        headers: {
            'X-CSRFToken': $('meta[name="csrf-token"]').attr('content')  // Add CSRF token to the headers
        }
    })
    .then(response => response.blob())
    .then(blob => {
        // Create a temporary link element to trigger the download
        const url = window.URL.createObjectURL(new Blob([blob]));
        const a = document.createElement('a');
        a.style.display = 'none';
        a.href = url;
        a.download = `${logName}.txt`; // Set the filename for download
        document.body.appendChild(a);
        a.click();
        window.URL.revokeObjectURL(url);
    })
    .catch(error => {
        console.error('Error downloading log:', error);
        alert('Error downloading log file.');
    });
}



// EMPTY LOG CONTENT
    function truncateLog() {
        const logSelect = document.getElementById('log-select');
        const logName = logSelect.options[logSelect.selectedIndex].value;
        fetch(`/services/logs/raw?log_name=${logName}`, {
            method: 'DELETE',
            headers: {
                'X-CSRFToken': $('meta[name="csrf-token"]').attr('content')  // Add CSRF token to the headers
            }
        })
        .then(response => response.json())
        .then(data => {
            alert(data.message);
        });
    }

    // Add event listener for truncate button
    document.addEventListener('DOMContentLoaded', function() {
        document.getElementById('truncate-btn').addEventListener('click', truncateLog);
    });




// SHOW RANDOM TIP ON START
        window.onload = function() {
            var logContent = document.getElementById('logs-tip-text');
            
            var messages = [
                ` You can add custom log files to this viewer. Guide: <a href="https://openpanel.co/docs/changelog/0.2.1/#log-viewer" target="_blank">https://openpanel.co/docs/changelog/0.2.1/#log-viewer</a>`,
                ` You can also download log files, just select a log and click on the '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="icon icon-tabler icons-tabler-outline icon-tabler-download"><path stroke="none" d="M0 0h24v24H0z" fill="none"></path><path d="M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2 -2v-2"></path><path d="M7 11l5 5l5 -5"></path><path d="M12 4l0 12"></path></svg> Download' button bellow the content.`,
                ` You can also empty the logs, just select a log file and click on the '<svg xmlns="http://www.w3.org/2000/svg" class="icon icon-tabler icon-tabler-trash" width="24" height="24" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" fill="none" stroke-linecap="round" stroke-linejoin="round"> <path stroke="none" d="M0 0h24v24H0z" fill="none"></path> <path d="M4 7l16 0"></path> <path d="M10 11l0 6"></path> <path d="M14 11l0 6"></path> <path d="M5 7l1 12a2 2 0 0 0 2 2h8a2 2 0 0 0 2 -2l1 -12"></path> <path d="M9 7v-3a1 1 0 0 1 1 -1h4a1 1 0 0 1 1 1v3"></path> </svg> Delete' button bellow the content.`,
                ` You can change the number of lines from the 'Lines' select. Value 'ALL' will display 1M lines.`
            ];

            var randomIndex = Math.floor(Math.random() * messages.length);
            logContent.innerHTML = messages[randomIndex];
            document.getElementById('logs-tip').classList.remove('d-none'); //show tip
        };
