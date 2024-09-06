document.addEventListener("DOMContentLoaded", function() {
    // Get the radio buttons and port input fields
    var domainRadio = document.querySelector('input[name="form-domain-name-is-set"]');
    var ipRadio = document.querySelector('input[name="form-domain-name-not-set-use-shared-ip"]');
    var adminPortInput = document.querySelector('input[name="2087_port"]');
    var adminPortPrepend = document.getElementById('2087_prepend');
    var openPortInput = document.querySelector('input[name="2083_port"]');
    var openPortPrepend = document.getElementById('2083_prepend');
    var sslCheckbox = document.getElementById('ssl_checkbox');


    // Add event listener for Force SSL checkbox
    sslCheckbox.addEventListener("change", function () {
        // Update the protocol in the prepend text based on the checkbox state
        updatePrependText();
    });

    // Add event listeners for radio buttons
    domainRadio.addEventListener("change", function() {
        if (domainRadio.checked) {
            // Update the prepend text for AdminPanel and OpenPanel ports
            updatePrependText();
        }
    });

    ipRadio.addEventListener("change", function() {
        if (ipRadio.checked) {
            // Update the prepend text for AdminPanel and OpenPanel ports
            var protocol = sslCheckbox.checked ? "https://" : "http://";
            adminPortPrepend.textContent = protocol + "{{ public_ip }}:";
            openPortPrepend.textContent = protocol + "{{ public_ip }}:";
        }
    });

    // Add event listener for custom domain input
    var domainInput = document.querySelector('input[name="force_domain"]');
    domainInput.addEventListener("input", function() {
        // If the user starts typing, check domainRadio and uncheck ipRadio
        if (!domainRadio.checked) {
            domainRadio.checked = true;
            ipRadio.checked = false;
            // Update the prepend text for AdminPanel and OpenPanel ports
            updatePrependText();
        }

        // If domainInput is empty, switch to ipRadio
        if (domainInput.value.trim() === "") {
            domainRadio.checked = false;
            ipRadio.checked = true;
            // Update the prepend text for AdminPanel and OpenPanel ports
            updatePrependText();
        } else {
            // Update the prepend text for AdminPanel and OpenPanel ports based on the custom domain input
            updatePrependText();
        }
    });

    


    // Function to update prepend text for AdminPanel and OpenPanel ports
    function updatePrependText() {
        var protocol = sslCheckbox.checked ? "https://" : "http://";
        // Retrieve the public IP address text from the element
        var publicIp = document.getElementById('public_ip_for_js').textContent;

        if (ipRadio.checked) {
            adminPortPrepend.textContent = protocol + publicIp + ":";
            openPortPrepend.textContent = protocol + publicIp + ":";
        }
        if (domainRadio.checked) {
            var customDomain = domainInput.value.trim();
            adminPortPrepend.textContent = protocol + customDomain + ":";
            openPortPrepend.textContent = protocol + customDomain + ":";
        }
    }

// UPDATE BUTTON
  $('#start_update_btn').click(function(e) {
    e.preventDefault();
    $.ajax({
      type: 'POST',
      url: '/settings/general/update_now',
      success: function(response) {
        $('#update_now_modal .modal-body').html('<div class="text-center py-4">' + response.message + '</div>');
        $('#update_now_modal .modal-footer').html('');
      },
      error: function(error) {
        $('#update_now_modal .modal-body').html('<div class="text-danger">Failed to start the update process. Please try again later.</div>');
      }
    });
  });


});


    document.addEventListener('DOMContentLoaded', function () {
        const customDomainCheckbox = document.getElementById('customDomain');
        const serverIPAddressCheckbox = document.getElementById('serverIPAddress');

        customDomainCheckbox.addEventListener('change', function () {
            serverIPAddressCheckbox.checked = false;
        });

        serverIPAddressCheckbox.addEventListener('change', function () {
            customDomainCheckbox.checked = false;
        });
    });


    document.addEventListener("DOMContentLoaded", function () {
        var form = document.querySelector('form');
        var toastContainer = document.createElement('div');
        toastContainer.classList.add('toast-container', 'position-fixed', 'bottom-0', 'end-0', 'p-3');
        document.body.appendChild(toastContainer);

        form.addEventListener('submit', function (event) {
            event.preventDefault(); // Prevent the default form submission

            // Perform AJAX submission
            var formData = new FormData(form);

            fetch(form.action, {
                method: form.method,
                body: formData,
            })
            .then(response => response.json())
            .then(data => {
                // Check for success messages
if (data.success_messages && data.success_messages.length > 0) {
    // Display success toasts
    displayToasts(data.success_messages, 'success');

    // Check for the specific success message
    if (data.success_messages.some(msg => msg.includes("Panels are accessible on IP address"))) {
        // Display a new toast for redirection message
        var portValue = document.querySelector('input[name="2087_port"]').value;
        var public_ip = "{{ public_ip }}";

        // Construct the redirect URL
        var redirectURL = `http://${public_ip}:${portValue}/settings/general`;

        displayToasts([`Redirecting to: ${redirectURL}`], 'info');

        // Redirect after 5 seconds
        setTimeout(function() {
            // Redirect the user
            window.location.href = redirectURL;
        }, 5000);
    } else if (data.success_messages.some(msg => msg.includes("is set as the domain name for both panels"))) {
        // Redirect for the new success message condition
        var portValue = document.querySelector('input[name="2087_port"]').value;
        var server_hostname = "{{ server_hostname }}";

        // Construct the redirect URL
        var redirectURL = `https://${server_hostname}:${portValue}/settings/general`;

        displayToasts([`Redirecting to: ${redirectURL}`], 'info');

        // Redirect after 5 seconds
        setTimeout(function() {
            // Redirect the user
            window.location.href = redirectURL;
        }, 5000);
    }
}


                // Check for error messages
                if (data.error_messages && data.error_messages.length > 0) {
                    displayToasts(data.error_messages, 'error');

    // Check for the specific error message when domain is set and already has a valid ssl
    if (data.error_messages.some(msg => msg.includes("Restarting the panel services to apply the newly generated SSL and force domain"))) {
        // Handle the error condition
        var portValue = document.querySelector('input[name="2087_port"]').value;
        var server_hostname = "{{ server_hostname }}";

        // Construct the redirect URL
        var redirectURL = `https://${server_hostname}:${portValue}/settings/general`;

        displayToasts([`Redirecting to: ${redirectURL}`], 'info');

        // Redirect after 5 seconds
        setTimeout(function() {
            // Redirect the user
            window.location.href = redirectURL;
        }, 5000);
    }










                }
            })
            .catch(error => console.error('Error:', error));
        });

        // Function to display Bootstrap Toasts
        function displayToasts(messages, type) {
            // Combine multiple messages into a single string
            var combinedMessage = messages.join('<br>');

            var toastDiv = document.createElement('div');
            toastDiv.classList.add('toast');
            toastDiv.setAttribute('role', 'alert');
            toastDiv.setAttribute('aria-live', 'assertive');
            toastDiv.setAttribute('aria-atomic', 'true');

            toastDiv.innerHTML = `
                <div class="toast-header">
                    <!-- Add SVG icons based on the message type -->
                    ${type === 'success' ? successIcon : type === 'info' ? infoIcon : dangerIcon}
                    <strong class="me-auto">${type.charAt(0).toUpperCase() + type.slice(1)}</strong>
                    <small>${new Date().toLocaleTimeString()}</small>
                    <button type="button" class="btn-close" data-bs-dismiss="toast" aria-label="Close"></button>
                </div>
                <div class="toast-body">
                    ${combinedMessage}
                </div>
            `;

            toastContainer.appendChild(toastDiv);

            // Use Bootstrap Toast to show the toast
            var toast = new bootstrap.Toast(toastDiv);
            toast.show();
        }

        // SVG icons for success and danger
        var successIcon = `
            <svg xmlns="http://www.w3.org/2000/svg" class="icon alert-icon" width="24" height="24" viewBox="0 0 24 24" stroke-width="2" stroke="green" fill="none" stroke-linecap="round" stroke-linejoin="round"><path stroke="none" d="M0 0h24v24H0z" fill="none"></path><path d="M5 12l5 5l10 -10"></path></svg>
        `;

        var infoIcon = `
            <svg xmlns="http://www.w3.org/2000/svg" class="icon alert-icon" width="24" height="24" viewBox="0 0 24 24" stroke-width="2" stroke="blue" fill="none" stroke-linecap="round" stroke-linejoin="round"><path stroke="none" d="M0 0h24v24H0z" fill="none"></path><path d="M3 12a9 9 0 1 0 18 0a9 9 0 0 0 -18 0"></path><path d="M12 9h.01"></path><path d="M11 12h1v4h1"></path></svg>
        `;

        var dangerIcon = `
            <svg xmlns="http://www.w3.org/2000/svg" class="icon alert-icon" width="24" height="24" viewBox="0 0 24 24" stroke-width="2" stroke="red" fill="none" stroke-linecap="round" stroke-linejoin="round"><path stroke="none" d="M0 0h24v24H0z" fill="none"></path><path d="M3 12a9 9 0 1 0 18 0a9 9 0 0 0 -18 0"></path><path d="M12 8v4"></path><path d="M12 16h.01"></path></svg>
        `;
    });



// CHEKC FOR UPDATES
const versionUrl = 'https://update.openpanel.com/';

fetch(versionUrl)
  .then(response => {
    if (!response.ok) {
      throw new Error('Network response was not ok');
    }
    return response.text();
  })
  .then(latestVersion => {
    const localVersion = document.getElementById('local_version').textContent;

    document.getElementById('latest_version').textContent = latestVersion;
    document.getElementById('version_in_modal').textContent = latestVersion;
    
    if (isVersionNewer(latestVersion, localVersion)) {
      document.getElementById('update_is_available').style.display = 'flex';
      document.getElementById('update_now_button').style.display = 'flex';
      document.getElementById('view_changelog').style.display = 'inline';
    }
  })
  .catch(error => {
    console.error('There was a problem with the fetch operation:', error);
  });

function isVersionNewer(version1, version2) {
  const v1 = version1.split('.').map(Number);
  const v2 = version2.split('.').map(Number);

  for (let i = 0; i < Math.max(v1.length, v2.length); i++) {
    const num1 = v1[i] || 0;
    const num2 = v2[i] || 0;
    
    if (num1 > num2) return true;
    if (num1 < num2) return false;
  }
  
  return false;
}
