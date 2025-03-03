    document.getElementById('post_to_enable_api_access').addEventListener('click', function(event) {
        var csrf_token = $('meta[name="csrf-token"]').attr('content');
        event.preventDefault();
        fetch('/settings/api', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/x-www-form-urlencoded',
                'X-CSRFToken': csrf_token,
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
