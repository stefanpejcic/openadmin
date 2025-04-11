document.addEventListener("DOMContentLoaded", function () {
    var form = document.getElementById('changePanelSettings');
    var toastContainer = document.createElement('div');
    var savePanelBtn = document.getElementById("save-openpanel-settings");
    toastContainer.classList.add('toast-container', 'position-fixed', 'bottom-0', 'end-0', 'p-3');
    document.body.appendChild(toastContainer);

    form.addEventListener('submit', function (event) {
        event.preventDefault(); // Prevent the default form submission

        savePanelBtn.disabled = true;
        savePanelBtn.innerHTML = '<span class="spinner-grow spinner-grow-sm" role="status" aria-hidden="true"></span>&nbsp; Saving changes...';

        // Perform AJAX submission
        var formData = new FormData(form);

        fetch(form.action, {
            method: form.method,
            body: formData,
        })
        .then(response => response.json())
        .then(data => {
            savePanelBtn.disabled = false;  // Enable the button
            savePanelBtn.innerHTML = 'Save';  // Reset the button text

            // Check for success messages
            if (data.success_messages && data.success_messages.length > 0) {
                displayToasts(data.success_messages, 'success');
            }

            // Check for error messages
            if (data.error_messages && data.error_messages.length > 0) {
                displayToasts(data.error_messages, 'error');
            }
        })
        .catch(error => {
            console.error('Error:', error);
            // Re-enable button and reset text in case of an error
            savePanelBtn.disabled = false;
            savePanelBtn.innerHTML = 'Save';
        });
    });

    // Function to display Bootstrap Toasts
    function displayToasts(messages, type) {
        var combinedMessage = messages.join('<br>');

        var toastDiv = document.createElement('div');
        toastDiv.classList.add('toast');
        toastDiv.setAttribute('role', 'alert');
        toastDiv.setAttribute('aria-live', 'assertive');
        toastDiv.setAttribute('aria-atomic', 'true');

        toastDiv.innerHTML = `
            <div class="toast-header">
                ${type === 'success' ? successIcon : dangerIcon}
                <strong class="me-auto">${type.charAt(0).toUpperCase() + type.slice(1)}</strong>
                <small>${new Date().toLocaleTimeString()}</small>
                <button type="button" class="btn-close" data-bs-dismiss="toast" aria-label="Close"></button>
            </div>
            <div class="toast-body">
                ${combinedMessage}
            </div>
        `;

        toastContainer.appendChild(toastDiv);
        var toast = new bootstrap.Toast(toastDiv);
        toast.show();
    }

    var successIcon = `
        <svg xmlns="http://www.w3.org/2000/svg" class="icon alert-icon" width="24" height="24" viewBox="0 0 24 24" stroke-width="2" stroke="green" fill="none" stroke-linecap="round" stroke-linejoin="round"><path stroke="none" d="M0 0h24v24H0z" fill="none"></path><path d="M5 12l5 5l10 -10"></path></svg>
    `;

    var dangerIcon = `
        <svg xmlns="http://www.w3.org/2000/svg" class="icon alert-icon" width="24" height="24" viewBox="0 0 24 24" stroke-width="2" stroke="red" fill="none" stroke-linecap="round" stroke-linejoin="round"><path stroke="none" d="M0 0h24v24H0z" fill="none"></path><path d="M3 12a9 9 0 1 0 18 0a9 9 0 0 0 -18 0"></path><path d="M12 8v4"></path><path d="M12 16h.01"></path></svg>
    `;
});

// Add an event listener to the checkboxes
var checkboxes = document.querySelectorAll('.form-selectgroup-input');
checkboxes.forEach(function (checkbox) {
    checkbox.addEventListener('change', updateSelectedModules);
});

// Function to update the hidden input field
function updateSelectedModules() {
    var selectedModules = [];
    checkboxes.forEach(function (checkbox) {
        if (checkbox.checked) {
            selectedModules.push(checkbox.value);
        }
    });
    document.getElementById('selectedModules').value = selectedModules.join(',');
}

window.addEventListener('load', updateSelectedModules);  // #222
