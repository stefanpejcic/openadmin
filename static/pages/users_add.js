$(document).ready(function() {
    const csrfToken = document.querySelector('meta[name="csrf-token"]').getAttribute('content');
    
    if (!csrfToken) {
        console.error("CSRF token is missing!");
        return;  // Exit if no CSRF token found
    }

    // IMPORT CP ACCOUNTS
    $('#userimportForm').on('submit', function(event) {
        event.preventDefault(); // Prevent the default form submission

        const formData = $(this).serialize(); // Serialize form data

        $.ajax({
            type: 'POST',
            url: $(this).attr('action'),
            headers: {
                'X-CSRFToken': csrfToken  // Add CSRF token to headers
            },
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

// Generate Random Username and Password
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
    const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*()-_=+";
    let result = "";
    for (let i = 0; i < length; i++) {
        const randomIndex = Math.floor(Math.random() * charset.length);
        result += charset.charAt(randomIndex);
    }
    return result;
}

// Form for New User
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

// Create a New User
document.getElementById("CreateUserButton").addEventListener("click", function() {
    var form = document.getElementById("userForm");

    // Validate the form
    if (form.checkValidity() === false) {
        form.reportValidity(); // This will show the validation errors
        return; // Stop the execution if the form is not valid
    }

    var createUserButton = document.getElementById("CreateUserButton");
    createUserButton.disabled = true;
    createUserButton.innerHTML = '<span class="animate-spin spinner-border spinner-border-sm" role="status" aria-hidden="true"></span>&nbsp; Creating user...';

    submitForm(form, false); // Initial form submission without debugging

    function submitForm(form, debug) {
        const csrfToken = document.querySelector('meta[name="csrf-token"]').getAttribute('content');
        var formData = new FormData(form);
        var statusMessageDiv = document.getElementById("statusMessage");
        statusMessageDiv.innerText = ""; // Clear previous messages

        const scrollinglogAreaDiv = document.getElementById("scrollinglogAreaDiv");
        scrollinglogAreaDiv.classList.remove("hidden");
        scrollinglogAreaDiv.classList.add("block"); // Tailwind 'block' class to display it

        fetch("/user/new", {
            method: "POST",
            headers: {
                'X-CSRFToken': csrfToken
            },
            body: formData
        }).then(response => {
            if (!response.ok) {
                throw new Error("Network response was not ok");
            }
            return response.body.getReader(); // Get the reader from the response body
        }).then(reader => {
            const decoder = new TextDecoder();
            const stream = new ReadableStream({
                start(controller) {
                    function read() {
                        reader.read().then(({ done, value }) => {
                            if (done) {
                                controller.close();
                                return;
                            }
                            const text = decoder.decode(value);
                            statusMessageDiv.innerText += text; // Append new message
                            read(); // Continue reading
                        });
                    }
                    read();
                }
            });

            return new Response(stream).text();
        }).then(text => {
            createUserButton.disabled = false;
            createUserButton.innerHTML = 'Create User';
            const response = JSON.parse(text);
            //scrollinglogAreaDiv.classList.remove("block");
            //scrollinglogAreaDiv.classList.add("hidden");

            if (response.message && response.message.includes("consider purchasing the Enterprise version")) {
                const updatedMessage = response.message.replace(
                    "consider purchasing the Enterprise version",
                    '<a href="https://openpanel.com/product/openpanel-premium-control-panel/" target="_blank" rel="noopener">consider purchasing the Enterprise version</a>'
                );
                statusMessageDiv.innerHTML = updatedMessage;
            }
        }).catch(error => {
            createUserButton.disabled = false;
            createUserButton.innerHTML = 'Create User';
            console.error('Fetch error:', error);
            //scrollinglogAreaDiv.classList.remove("block");
            //scrollinglogAreaDiv.classList.add("hidden");
        });
    }
});
