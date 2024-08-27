// IMPORT CP ACCOUNTS
        $(document).ready(function() {
            $('#userimportForm').on('submit', function(event) {
                event.preventDefault(); // Prevent the default form submission

                const formData = $(this).serialize(); // Serialize form data

                $.ajax({
                    type: 'POST',
                    url: $(this).attr('action'),
                    data: formData,
                    success: function(response) {
                        if (response.status === 'success') {
                            window.location.href = '/import/cpanel'; // Redirect on success
                        } else {
                            alert(response.message); // Show error message
                        }
                    },
                    error: function(xhr, status, error) {
                        alert('An error occurred: ' + error);
                    }
                });
            });
        });



// FILTER USERS TABLE

function updateTableRows(searchTerm) {
    const rows = document.querySelectorAll('.table tbody tr');
    rows.forEach(row => {
        const username = row.getAttribute('data-username').toLowerCase();
        const email = row.getAttribute('data-email').toLowerCase();

        if (username.includes(searchTerm.toLowerCase()) || email.includes(searchTerm.toLowerCase())) {
            row.style.display = '';
        } else {
            row.style.display = 'none';
        }
    });
}
const searchInput = document.getElementById('userSearchInput');

if (searchInput) {
    searchInput.addEventListener('input', function() {
        const searchTerm = this.value.trim();
        updateTableRows(searchTerm);
    });
}




// SHOW DEDI IP ADDRESS IN USERS TABLE

function updatePublicIP(username, ip) {
    const ipElements = document.querySelectorAll(`[data-username="${username}"] .public-ip`);
    ipElements.forEach(element => {
        element.textContent = ip;
    });
}


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

document.getElementById("togglePassword").addEventListener("click", function() {
    const passwordField = document.getElementById("admin_password");
    if (passwordField.type === "password") {
        passwordField.type = "text";
    } else {
        passwordField.type = "password";
    }
});


// CREATE A NEW USER

document.getElementById("CreateUserButton").addEventListener("click", function() {

    var form = document.getElementById("userForm");

    // Validate the form
    if (form.checkValidity() === false) {
        form.reportValidity(); // This will show the validation errors
        return; // Stop the execution if the form is not valid
    }

    var createUserButton = document.getElementById("CreateUserButton");
    createUserButton.disabled = true;
    createUserButton.innerHTML = '<span class="spinner-grow spinner-grow-sm" role="status" aria-hidden="true"></span>&nbsp; Creating user...';

    var userCreatedSuccessfully = false; // flag to reload after creating user and closing modal

    submitForm(form, false); // Initial form submission without debugging

    function submitForm(form, debug) {
        if (debug) {
            // Add debug flag if not already present
            if (!form.querySelector('input[name="debug"]')) {
                var debugInput = document.createElement('input');
                debugInput.type = 'hidden';
                debugInput.name = 'debug';
                debugInput.value = '1';
                form.appendChild(debugInput);
            }
        }

        var formData = new FormData(form);
        var xhr = new XMLHttpRequest();
        xhr.open("POST", "/user/new", true);

        xhr.onload = function() {
            createUserButton.disabled = false;
            createUserButton.innerHTML = 'Create User';

            var response = JSON.parse(xhr.responseText);

            if (xhr.status === 200 && response.success) {
                userCreatedSuccessfully = true; // reload page after modal is closed
                displayResponseModal(true, response.response.message);
                if (response.response.message.includes("Successfully")) {
                    form.reset();
                }
            } else {
                if (debug) {
                    displayResponseModal(false, response.error, true);
                } else {
                    displayResponseModal(false, response.error, false);
                }
            }
        };

        xhr.send(formData);
    }

    function displayResponseModal(success, message, isDebug) {
        var modal = document.getElementById("responseModal");
        var modalTitle = modal.querySelector(".modal-title");
        var modalBody = document.getElementById("responseModalBody");
        var modalFooter = modal.querySelector(".modal-footer");

        modalTitle.innerHTML = success ? "New user created successfully" : "Error: Creating user failed!";
        
        // link to enterprise if limit of 3 users is reached
        if (message.includes("consider purchasing the Enterprise version")) {
            message = message.replace(
                "consider purchasing the Enterprise version",
                '<a href="https://openpanel.com/product/openpanel-premium-control-panel/?utm=upsell_3users_limit_on_free" target="_blank">consider purchasing the Enterprise version</a>'
            );
            modalBody.innerHTML = message; // html to display link to enterprise
        } else if (isDebug) {
            modalBody.innerHTML = "<pre>" + message + "</pre>"; // pre tag to not break html 
        } else {
            modalBody.textContent = message; // standard text is default
        }

       if (!success && !isDebug && !message.includes("limit of 3 user account")) {
            var retryButton = document.createElement("button");
            retryButton.className = "btn btn-warning";
            retryButton.innerHTML = "Retry with debugging";
            retryButton.addEventListener("click", function() {
                submitForm(form, true);
                $(modal).modal('hide');
            });
            modalFooter.innerHTML = ""; // Clear previous footer content
            modalFooter.appendChild(retryButton);
        } else {
            modalFooter.innerHTML = ""; // Clear the footer if successful or if retry is not allowed
        }

        $(modal).modal("show");
    }

    $('#responseModal').on('hide.bs.modal', function () {
        // Remove the debug input field if the modal is closed
        var debugInput = form.querySelector('input[name="debug"]');
        if (debugInput) {
            debugInput.remove();
        }

        // Hide the retry button if it's still visible
        var modalFooter = document.getElementById("responseModal").querySelector(".modal-footer");
        modalFooter.innerHTML = ""; // Clear the footer content
    });
});



// RELOAD USERS PAGE IF MODAL IS CLOSED AFTER CREATING A USER
document.addEventListener('DOMContentLoaded', function() {
    var modal = document.getElementById("responseModal");

    modal.addEventListener('hidden.bs.modal', function (e) {
        if (userCreatedSuccessfully) {
            location.reload();
        }
    });
});



// DISPLAY USER ONLINE STATUS

fetch('/json/user_activity_status')
  .then(response => response.json())
  .then(data => {
    document.querySelectorAll('tbody tr').forEach(row => {
      const username = row.getAttribute('data-username');
      const status = data[username];

      if (status === 'active') {
        const onlineDiv = document.createElement('div');
        onlineDiv.classList.add('small', 'mt-1');

        const aTag = document.createElement('a');
        aTag.setAttribute('href', `/users/${username}#nav-activity`);
        aTag.setAttribute('style', 'text-decoration: none;');
        onlineDiv.appendChild(aTag);

        const spanBadge = document.createElement('span');
        spanBadge.classList.add('badge', 'bg-green');
        aTag.appendChild(spanBadge);

        const avatarSpan = row.querySelector('span.avatar.me-2');
        if (avatarSpan) {
          avatarSpan.appendChild(onlineDiv);
        }
      }
    });
  })
  .catch(error => console.error('Error fetching user activity status:', error));


// TRIGER NEW SUER FORM ON PARAM

document.addEventListener('DOMContentLoaded', function() {
    // Check if the URL contains '#new'
    if (window.location.href.indexOf('#new') > -1) {
        // If '#new' is present, find the button by its ID and trigger a click event
        var addUserButton = document.getElementById('addUserButton');
        if (addUserButton) {
            addUserButton.click(); // Trigger click event
        } else {
            console.error('Button with ID "addUserButton" not found.');
        }
    }
});
