    document.getElementById('post_to_enable_api_access').addEventListener('click', function(event) {
        event.preventDefault();
        fetch('/settings/api', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/x-www-form-urlencoded',
            },
            body: 'action=on'
        })
        .then(response => {
            if (response.ok) {
                window.location.reload();
            } else {
                response.json().then(data => alert(data.error));
            }
        })
        .catch(error => console.error('Error:', error));
    });
