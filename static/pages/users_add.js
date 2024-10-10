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
    const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*()-_=+";
    let result = "";
    for (let i = 0; i < length; i++) {
        const randomIndex = Math.floor(Math.random() * charset.length);
        result += charset.charAt(randomIndex);
    }
    return result;
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
        var formData = new FormData(form);
        var statusMessageDiv = document.getElementById("statusMessage");
        statusMessageDiv.innerText = ""; // Clear previous messages

        const scrollinglogAreaDiv = document.getElementById("scrollinglogAreaDiv");
        scrollinglogAreaDiv.classList.remove("d-none");
        scrollinglogAreaDiv.classList.add("active");

        fetch("/user/new", {
            method: "POST",
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
                            statusMessageDiv.scrollTop = statusMessageDiv.scrollHeight; // Auto scroll
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
            scrollinglogAreaDiv.classList.remove("active");

        }).catch(error => {
            createUserButton.disabled = false;
            createUserButton.innerHTML = 'Create User';
            console.error('Fetch error:', error);
            scrollinglogAreaDiv.classList.remove("active");
        });
    }
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


