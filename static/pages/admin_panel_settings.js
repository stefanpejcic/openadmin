document.addEventListener("DOMContentLoaded", function() {
  var adminDeleteButton = document.getElementById("AdminDeleteButton");
  var deleteAdminConfirmation = document.getElementById("deleteAdminConfirmation");
  var confirmAdminDelete = document.getElementById("confirmAdminDelete");

  if (adminDeleteButton) {
    adminDeleteButton.addEventListener("click", function(event) {
      event.preventDefault();
      // Clear the input field and hide the Disable button when the modal is shown
      if (deleteAdminConfirmation) deleteAdminConfirmation.value = "";
      if (confirmAdminDelete) confirmAdminDelete.style.display = "none";
    });
  }

  if (deleteAdminConfirmation) {
    deleteAdminConfirmation.addEventListener("input", function() {
      // Toggle the Disable button visibility based on the input value
      var confirmationInput = this.value.trim().toUpperCase();
      if (confirmAdminDelete) {
        confirmAdminDelete.style.display = (confirmationInput === "DELETE") ? "block" : "none";
      }
    });
  }
});



document.getElementById("confirmAdminDelete").addEventListener("click", function() {
  // Proceed with the termination

  var deleteModalxClose = document.getElementById("deleteAdminModalxClose");
  var confirmAdminOffModal = document.getElementById("confirmAdminOffModal");
  var modalFooter = document.getElementById("confirmAdminOffModal").getElementsByClassName("modal-footer")[0];

  confirmAdminOffModal.setAttribute("data-bs-backdrop", "static");
  confirmAdminOffModal.setAttribute("data-bs-keyboard", "false");
  modalFooter.style.display = "none";
  deleteModalxClose.style.display = "none";
  // Update modal content to indicate deletion is in progress
  var modalBody = document.getElementById("confirmAdminOffModal").getElementsByClassName("modal-body")[0];
  modalBody.innerHTML = ``;


  fetch(`/settings/open-admin/users`, {
      method: "DELETE",
        headers: {
            'X-CSRFToken': $('meta[name="csrf-token"]').attr('content')  // Add CSRF token to the headers
        }  
  })
  .catch(error => {
      console.error("Error sending request:", error);
  });


});

document.getElementById("generate_server_report").addEventListener("click", function() {
  const generateReportButton = document.getElementById('generate_server_report');
    generateReportButton.disabled = true;
    generateReportButton.innerHTML = '<span class="spinner-grow spinner-grow-sm" role="status" aria-hidden="true"></span>&nbsp; Generating report...';


    fetch(`/settings/open-admin`, {
        method: "POST",
        body: new URLSearchParams({
            'action': 'server_info'
        }),
        headers: {
            'Content-Type': 'application/x-www-form-urlencoded'
        },
    })
    .then(response => response.json())
    .then(data => {
        const reportResponseDiv = document.getElementById("generated_report_response");
        if (data.success_messages && data.success_messages.length > 0) {
            generateReportButton.disabled = false;
            generateReportButton.innerHTML = 'Generate a new report';

            const urlRegex = /(http[s]?:\/\/[^\s]+\.txt)/;
            const matches = data.success_messages[0].match(urlRegex);
            if (matches && matches.length > 0) {
                const link = document.createElement('a');
                link.href = matches[0]; // The first match should be the URL

                // Splitting the URL by '/' and selecting the last part for the link text
                const parts = matches[0].split('/');
                link.textContent = parts[parts.length - 1]; // The last part of the URL

                link.target = "_blank"; // Opens the link in a new tab
                reportResponseDiv.innerHTML = ''; // Clear previous content
                reportResponseDiv.appendChild(link);
            } else {
                // If no URL is found, check if the message contains a path like "/var/log/..."
                const pathRegex = /(\/var\/log\/[^\s]+)/;
                const pathMatch = data.success_messages[0].match(pathRegex);
                if (pathMatch && pathMatch.length > 0) {
                    // If a path is found, display it in a <code> block
                    const codeElement = document.createElement('code');
                    codeElement.textContent = pathMatch[0]; // The matched path

                    reportResponseDiv.innerHTML = ''; // Clear previous content
                    reportResponseDiv.appendChild(codeElement);
                } else {
                    // If no URL or path is found, just display the message
                    reportResponseDiv.textContent = data.success_messages[0];
                }
            }
        } else {
            console.error("Error generating report: No success messages received.");
            generateReportButton.disabled = false;
            generateReportButton.innerHTML = 'Click to try again';
            reportResponseDiv.textContent = "Error generating report: No success messages received.";
        }
    })
    .catch(error => {
        console.error("Error sending request:", error);
        const reportResponseDiv = document.getElementById("generated_report_response");
        generateReportButton.disabled = false;
        generateReportButton.innerHTML = 'Generate a new report';
        reportResponseDiv.textContent = "Error sending request: " + error;
    });
});









// DELETE ADMIN USER

  
  // Function to handle click event on delete button
  document.addEventListener('DOMContentLoaded', function() {
    var deleteButtons = document.querySelectorAll('a.btn.btn-danger');
    deleteButtons.forEach(function(button) {
      button.addEventListener('click', function(event) {
        event.preventDefault();
        var username = this.closest('tr').querySelector('.font-weight-medium').childNodes[0].textContent.trim();
        document.getElementById('admin_user_to_delete').textContent = username;
        document.getElementById('deleteAdminUserConfirmation').value = '';
        document.getElementById('confirmUserDelete').style.display = 'none';
        $('#confirmAdminDeleteModal').modal('show');
      });
    });
    
    // Function to handle input in delete confirmation field
    document.getElementById('deleteAdminUserConfirmation').addEventListener('input', function() {
      var confirmationInput = this.value.trim().toUpperCase(); // Convert input to uppercase
      if (confirmationInput === 'DELETE') { // Check if input matches "DELETE"
        document.getElementById('confirmUserDelete').style.display = 'block';
      } else {
        document.getElementById('confirmUserDelete').style.display = 'none';
      }
    });
    
    // Function to handle click event on confirm delete button
    document.getElementById('confirmUserDelete').addEventListener('click', function() {
      var username = document.getElementById('admin_user_to_delete').textContent.trim();
      fetch('/settings/open-admin/users', {
        method: 'DELETE',
        headers: {
          'Content-Type': 'application/json',
          'X-CSRFToken': $('meta[name="csrf-token"]').attr('content')  // Add CSRF token to the headers
        },
        body: JSON.stringify({ username: username })
      })
      .then(function(response) {
        if (response.ok) {
          window.location.reload(); // Reload page on success
        } else {
          throw new Error('Failed to delete user');
        }
      })
      .catch(function(error) {
        console.error('Error:', error);
        // Handle error here, e.g., display an error message to the user
      });
    });
  });

















document.getElementById("AdminOffButton").addEventListener("click", function(event) {
  event.preventDefault();
  // Clear the input field and hide the Disable button when the modal is shown
  document.getElementById("deleteConfirmation").value = "";
  document.getElementById("confirmDelete").style.display = "none";
});

document.getElementById("deleteConfirmation").addEventListener("input", function() {
  // Toggle the Disable button visibility based on the input value
  var confirmationInput = this.value.trim().toUpperCase();
  document.getElementById("confirmDelete").style.display = (confirmationInput === "DISABLE") ? "block" : "none";
});


document.getElementById("confirmDelete").addEventListener("click", function() {
  // Proceed with the termination

  var deleteModalxClose = document.getElementById("deleteModalxClose");
  var confirmAdminOffModal = document.getElementById("confirmAdminOffModal");
  var modalFooter = document.getElementById("confirmAdminOffModal").getElementsByClassName("modal-footer")[0];

  confirmAdminOffModal.setAttribute("data-bs-backdrop", "static");
  confirmAdminOffModal.setAttribute("data-bs-keyboard", "false");
  modalFooter.style.display = "none";
  deleteModalxClose.style.display = "none";
  // Update modal content to indicate deletion is in progress
  var modalBody = document.getElementById("confirmAdminOffModal").getElementsByClassName("modal-body")[0];
  modalBody.innerHTML = `
    <div class="text-center">
      <div class="text-secondary mb-3">Disabling OpenAdmin service, please wait..</div>
      <div class="progress progress-sm">
        <div class="progress-bar progress-bar-indeterminate"></div>
      </div>
    </div>
  `;


  fetch(`/settings/open-admin`, {
      method: "POST",
      body: new URLSearchParams({
          'action': 'admin_off'
      }),
      headers: {
          'Content-Type': 'application/x-www-form-urlencoded',
          'X-CSRFToken': $('meta[name="csrf-token"]').attr('content')  // Add CSRF token to the headers
      },
  })
  .then(response => response.json())
  .then(data => {
      if (data.message.includes("successfully")) {
          window.location.reload();
      } else {
          console.error("Error disabling OpenAdmin:", data.message);
      }
  })
  .catch(error => {
      console.error("Error sending request:", error);
  });


});



// FORM FOR NEW USER
window.onload = function() {
    const generatedUsername = generateRandomUsername(8);
    const generatedPassword = generateRandomStrongPassword(16);

    document.getElementById("admin_username").value = generatedUsername;
    document.getElementById("admin_password").value = generatedPassword;
};

document.getElementById("generatePassword").addEventListener("click", function() {
    const generatedPassword = generateRandomStrongPassword(16);
    document.getElementById("admin_password").value = generatedPassword;
    const passwordField = document.getElementById("admin_password");
    if (passwordField.type === "password") {
      passwordField.type = "text";
    }
});

document.getElementById("generatePasswordEdit").addEventListener("click", function() {
    const generatedPassword = generateRandomStrongPassword(16);
    document.getElementById("new_password").value = generatedPassword;
    const passwordField = document.getElementById("new_password");
    if (passwordField.type === "password") {
      passwordField.type = "text";
    }
});


document.getElementById("togglePasswordEdit").addEventListener("click", function() {
    const passwordField = document.getElementById("new_password");
    const icon = this.getElementsByTagName('svg')[0];

    if (passwordField.type === "password") {
        passwordField.type = "text";
        // Update the SVG to 'eye-off' icon for showing password
        icon.innerHTML = '<path stroke="none" d="M0 0h24v24H0z" style="margin:0" fill="none"/><path d="M10.585 10.587a2 2 0 0 0 2.829 2.828" /><path d="M16.681 16.673a8.717 8.717 0 0 1 -4.681 1.327c-3.6 0 -6.6 -2 -9 -6c1.272 -2.12 2.712 -3.678 4.32 -4.674m2.86 -1.146a9.055 9.055 0 0 1 1.82 -.18c3.6 0 6.6 2 9 6c-.666 1.11 -1.379 2.067 -2.138 2.87" /><path d="M3 3l18 18" />';
    } else {
        passwordField.type = "password";
        // Update the SVG back to 'eye' icon for hiding password
        icon.innerHTML = '<path stroke="none" d="M0 0h24v24H0z" style="margin:0" fill="none"/><path d="M10 12a2 2 0 1 0 4 0a2 2 0 0 0 -4 0" /><path d="M21 12c-2.4 4 -5.4 6 -9 6c-3.6 0 -6.6 -2 -9 -6c2.4 -4 5.4 -6 9 -6c3.6 0 6.6 2 9 6" />';
    }
});



document.getElementById("togglePassword").addEventListener("click", function() {
    const passwordField = document.getElementById("admin_password");
    const icon = this.getElementsByTagName('svg')[0];

    if (passwordField.type === "password") {
        passwordField.type = "text";
        // Update the SVG to 'eye-off' icon for showing password
        icon.innerHTML = '<path stroke="none" d="M0 0h24v24H0z" style="margin:0" fill="none"/><path d="M10.585 10.587a2 2 0 0 0 2.829 2.828" /><path d="M16.681 16.673a8.717 8.717 0 0 1 -4.681 1.327c-3.6 0 -6.6 -2 -9 -6c1.272 -2.12 2.712 -3.678 4.32 -4.674m2.86 -1.146a9.055 9.055 0 0 1 1.82 -.18c3.6 0 6.6 2 9 6c-.666 1.11 -1.379 2.067 -2.138 2.87" /><path d="M3 3l18 18" />';
    } else {
        passwordField.type = "password";
        // Update the SVG back to 'eye' icon for hiding password
        icon.innerHTML = '<path stroke="none" d="M0 0h24v24H0z" style="margin:0" fill="none"/><path d="M10 12a2 2 0 1 0 4 0a2 2 0 0 0 -4 0" /><path d="M21 12c-2.4 4 -5.4 6 -9 6c-3.6 0 -6.6 -2 -9 -6c2.4 -4 5.4 -6 9 -6c3.6 0 6.6 2 9 6" />';
    }
});


// GENERATE RANDOM USERNAME AND PASSWORD

function generateRandomUsername(length) {
    const charset = "abcdefghijklmnopqrstuvwxyz0123456789";
    let result = "";
    for (let i = 0; i < length; i++) {
        const randomIndex = Math.floor(Math.random() * charset.length);
        result += charset.charAt(randomIndex);
    }
    return result;
}

function generateRandomStrongPassword(length) {
    const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";
    let result = "";
    for (let i = 0; i < length; i++) {
        const randomIndex = Math.floor(Math.random() * charset.length);
        result += charset.charAt(randomIndex);
    }
    return result;
}


// CREATE NEW ADMIN USER

function createAdminUser() {
  var csrf_token = $('meta[name="csrf-token"]').attr('content');
  // Capture username and password values
  const username = document.getElementById('admin_username').value;
  const password = document.getElementById('admin_password').value;
  const role = document.getElementById('role').value;

  // Prepare the data to be sent in the request
  const data = { username, password, role };

  // Send a POST request to the Flask route
  fetch('/settings/open-admin/users', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-CSRFToken': $('meta[name="csrf-token"]').attr('content')  // Add CSRF token to the headers
    },
    body: JSON.stringify(data),
  })
  .then(response => response.json())
  .then(data => {
    // Update the modal body with the response
    const modalBody = document.querySelector('#modal-newuser .modal-body');
    modalBody.innerHTML = `<p>${data.success ? 'Admin user created successfully' : 'Failed to create admin user: ' + data.error}</p>`;
    
    // If the user was successfully created, reload the page
    if (data.success) {
      window.location.reload();
    }
  })
  .catch((error) => {
    console.error('Error:', error);
  });
}



// Function to handle edit user form submission
function editAdminUser() {
    // Get form input values
    var username = document.getElementById('new_username').value.trim();
    var newPassword = document.getElementById('new_password').value.trim();
    var isActive = document.getElementById('is_active').checked;

    // Array to store promises for asynchronous requests
    var requests = [];

    // Check if username is changed
    var originalUsername = document.getElementById('username_to_edit').textContent.trim();
    if (username !== originalUsername) {
        requests.push(sendRenameUserRequest(originalUsername, username));
    }

    // Check if new password is provided
    if (newPassword !== '') {
        requests.push(sendResetPasswordRequest(username, newPassword));
    }

    // Check if active status is changed
    var originalActiveStatus = document.getElementById('is_active').getAttribute('data-original') === 'true';
    if (isActive !== originalActiveStatus) {
        if (isActive) {
            requests.push(sendUnsuspendUserRequest(username));
        } else {
            requests.push(sendSuspendUserRequest(username));
        }
    }

    // Wait for all requests to complete
    Promise.all(requests)
    .then(function(responses) {
        // Handle success response
        // For example, close the modal and refresh the page
        $('#modal-edituser').modal('hide');
        window.location.reload();
    })
    .catch(function(error) {
        console.error('Error:', error);
        // Handle error response
        // For example, display an error message to the user
    });
}

// Function to send rename user request
function sendRenameUserRequest(originalUsername, newUsername) {
    var data = {
        action: 'rename_user',
        username: originalUsername,
        new_username: newUsername
    };
    return fetch('/settings/open-admin/users', {
        method: 'PATCH',
        headers: {
            'Content-Type': 'application/json',
            'X-CSRFToken': $('meta[name="csrf-token"]').attr('content')  // Add CSRF token to the headers
        },
        body: JSON.stringify(data)
    });
}

// Function to send reset password request
function sendResetPasswordRequest(username, newPassword) {
    var data = {
        action: 'reset_password',
        username: username,
        new_password: newPassword
    };
    return fetch('/settings/open-admin/users', {
        method: 'PATCH',
        headers: {
            'Content-Type': 'application/json',
            'X-CSRFToken': $('meta[name="csrf-token"]').attr('content')  // Add CSRF token to the headers
        },
        body: JSON.stringify(data)
    });
}

// Function to send suspend user request
function sendSuspendUserRequest(username) {
    var data = {
        action: 'suspend_user',
        username: username
    };
    return fetch('/settings/open-admin/users', {
        method: 'PATCH',
        headers: {
            'Content-Type': 'application/json',
            'X-CSRFToken': $('meta[name="csrf-token"]').attr('content')  // Add CSRF token to the headers
        },
        body: JSON.stringify(data)
    });
}

// Function to send unsuspend user request
function sendUnsuspendUserRequest(username) {
    var data = {
        action: 'unsuspend_user',
        username: username
    };
    return fetch('/settings/open-admin/users', {
        method: 'PATCH',
        headers: {
            'Content-Type': 'application/json',
            'X-CSRFToken': $('meta[name="csrf-token"]').attr('content')  // Add CSRF token to the headers
        },
        body: JSON.stringify(data)
    });
}

