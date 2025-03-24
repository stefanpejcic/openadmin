$(document).ready(function() {
    
    function fetchLogContent() {
        $.ajax({
            url: '/settings/api?view=api_log',
            type: 'GET',
            success: function(data) {
                $('#log_content').text(data);
            },
            error: function(error) {
                $('#log_content').text('Failed to fetch log content.');
            }
        });
    }

    fetchLogContent();
    setInterval(fetchLogContent, 15000);
});


document.addEventListener("DOMContentLoaded", function() {
    var csrf_token = $('meta[name="csrf-token"]').attr('content');
    const methodSelect = document.querySelector('select[name="method"]');
    const urlInput = document.querySelector('input[name="url"]');
    const tokenDiv = document.getElementById('token');
    const usernameDiv = document.getElementById('username');
    const passwordDiv = document.getElementById('password');
    
    // Add event listener to Method select
    methodSelect.addEventListener('change', function() {
        const selectedMethod = methodSelect.value;
        if (selectedMethod !== 'GET') {
            tokenDiv.style.display = 'block';
            if (selectedMethod === 'POST' && !urlInput.value.trim()) {
                usernameDiv.style.display = 'block';
                passwordDiv.style.display = 'block';
                tokenDiv.style.display = 'none';
            } else {
                usernameDiv.style.display = 'none';
                passwordDiv.style.display = 'none';
                tokenDiv.style.display = 'block';
            }
        } else {
            tokenDiv.style.display = 'block';
            usernameDiv.style.display = 'none';
            passwordDiv.style.display = 'none';
        }
    });

    // Add event listener to URL input
    urlInput.addEventListener('input', function() {
        const selectedMethod = methodSelect.value;
        if (selectedMethod === 'POST' && !urlInput.value.trim()) {
            usernameDiv.style.display = 'block';
            passwordDiv.style.display = 'block';
            tokenDiv.style.display = 'none';
        } else if (selectedMethod === 'GET' && !urlInput.value.trim()) {
            usernameDiv.style.display = 'none';
            passwordDiv.style.display = 'none';
            tokenDiv.style.display = 'none';
        }
        
        else {
            usernameDiv.style.display = 'none';
            passwordDiv.style.display = 'none';
            tokenDiv.style.display = 'block';
        }
    });

    // Trigger initial change event to apply initial state
    methodSelect.dispatchEvent(new Event('change'));
});


document.getElementById('httpRequestForm').addEventListener('submit', function(e) {
    var csrf_token = $('meta[name="csrf-token"]').attr('content');
    e.preventDefault();

    document.getElementById('submit_button').style.display = 'none';
    document.getElementById('working').style.display = 'inline';

    const method = this.querySelector('select[name="method"]').value;
    let path = this.querySelector('input[name="url"]').value;
    if (!path.startsWith('/api/')) {
        path = '/api/' + path;
    }

    let headers = {
        'Content-Type': 'application/json',
        'X-CSRFToken': csrf_token
    };

    // Start building the cURL command
    const baseUrl = `${location.protocol}//${location.hostname}${location.port ? ':' + location.port : ''}`;
    const fullUrl = baseUrl + path;
    let curlCommand = `curl -X ${method} "${fullUrl}"`;

    let requestBody = null;

    // Handle JSON body for non-GET requests other than /api/
    if (method === 'POST' && path === '/api/') {
        // For POST to /api/, include username and password in the JSON body
        const username = this.querySelector('input[name="username"]').value;
        const password = this.querySelector('input[name="password"]').value;
        requestBody = JSON.stringify({username: username, password: password});
        curlCommand += ' -H "Content-Type: application/json" -d \'' + requestBody + '\'';
    } else if (method === 'POST') {
        // For POST to /api/$something, include the token + json data
        const tokenValue = this.querySelector('input[name="token"]').value;
        if (tokenValue) {
            headers['Authorization'] = `Bearer ${tokenValue}`;
            curlCommand += ` -H "Authorization: Bearer ${tokenValue}"`;
            //requestBody = document.querySelector('.json_data').value;
            curlCommand += ' -H "Content-Type: application/json"';
            requestBody = JSON.stringify(getRequestBody());
            if (requestBody && requestBody !== '{}') { // Ensure non-empty body
                curlCommand += ` -d '${requestBody}'`;
            }
        }
    } else if (method !== 'GET' && path !== '/api/') {
        requestBody = document.querySelector('.json_data').value;
        if (requestBody) {
            headers['Content-Type'] = 'application/json';
            curlCommand += ' -H "Content-Type: application/json"';
            curlCommand += ` -d '${requestBody}'`;
        }
    } else if (method === 'GET' && path !== '/api/') {
        const tokenValue = this.querySelector('input[name="token"]').value;
        if (tokenValue) {
            headers['Authorization'] = `Bearer ${tokenValue}`;
            curlCommand += ` -H "Authorization: Bearer ${tokenValue}"`;
        }
    }

    document.getElementById('requestDetails').innerText = curlCommand;

    fetch(path, {
        method: method,
        headers: headers,
        body: (method !== 'GET' && requestBody) ? requestBody : undefined
    })
    //.then(response => response.text()) // need 4 errors
    .then(response => response.json())
    .then(data => {
        document.getElementById('submit_button').style.display = 'inline';
        document.getElementById('working').style.display = 'none';
        //document.getElementById('responseDisplay').innerText = data;
        document.getElementById('responseDisplay').innerText = JSON.stringify(data, null, 2);
    })
    .catch(error => {
        document.getElementById('submit_button').style.display = 'inline';
        document.getElementById('working').style.display = 'none';
        document.getElementById('responseDisplay').innerText = 'Error: ' + error;
    });
});


function getRequestBody() {
    // Construct request body based on visible fields
    let requestBody = {};

    // Parse JSON content from textarea and add key-value pairs
    const jsonContent = document.querySelector('.json_data').value.trim();
    if (jsonContent) {
        const keyValuePairs = jsonContent.split(/,|\n/).map(pair => pair.trim().split(':'));
        keyValuePairs.forEach(pair => {
            if (pair.length === 2) { // Check if pair has two elements
                const key = pair[0].trim().replace(/"/g, '');
                const value = pair[1].trim().replace(/"/g, '');
                requestBody[key] = value;
            }
        });
    }


    return requestBody;
}

