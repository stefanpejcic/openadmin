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
    const generatedPassword = generateRandomStrongPassword(16);
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

    const username = document.getElementById('admin_username').value;
    const password = document.getElementById('admin_password').value;


// Create alert element
const helpBtn = document.getElementById('helpButton');
helpBtn.style.display = 'none';


// Create new login button/link
const newLink = document.createElement('div');
newLink.classList.add('flex', 'items-center');

newLink.innerHTML = `
  <a
    href="/login/token?username=${encodeURIComponent(username)}"
    target="_blank"
    class="ml-2 relative inline-flex items-center justify-center rounded-md border p-2 text-center text-sm font-medium shadow-sm transition-all duration-100 ease-in-out cursor-pointer outline outline-offset-2 outline-0 focus-visible:outline-2 outline-blue-500 dark:outline-blue-500 dark:border-gray-800 dark:text-gray-50 dark:bg-gray-950 dark:hover:bg-gray-900/60 disabled:text-gray-400 disabled:dark:text-gray-600 sm:w-fit border-black bg-black text-white hover:bg-gray-800 sm:mt-0"
    x-data="{ spinning: false }"
    @click="spinning = true; setTimeout(() => spinning = false, 3000)"
  >
    <svg
      version="1.0"
      style="vertical-align: middle;"
      xmlns="http://www.w3.org/2000/svg"
      width="16"
      height="18"
      viewBox="0 0 213.000000 215.000000"
      preserveAspectRatio="xMidYMid meet"
      :class="{'animate-spin': spinning}"
      class=""
    >
      <g transform="translate(0.000000,215.000000) scale(0.100000,-0.100000)" fill="currentColor" stroke="none">
        <path d="M990 2071 c-39 -13 -141 -66 -248 -129 -53 -32 -176 -103 -272 -158 -206 -117 -276 -177 -306 -264 -17 -50 -19 -88 -19 -460 0 -476 0 -474 94 -568 55 -56 124 -98 604 -369 169 -95 256 -104 384 -37 104 54 532 303 608 353 76 50 126 113 147 184 8 30 12 160 12 447 0 395 -1 406 -22 461 -34 85 -98 138 -317 264 -104 59 -237 136 -295 170 -153 90 -194 107 -275 111 -38 2 -81 0 -95 -5z m205 -561 c66 -38 166 -95 223 -127 l102 -58 0 -262 c0 -262 0 -263 -22 -276 -13 -8 -52 -31 -88 -51 -36 -21 -126 -72 -200 -115 l-135 -78 -3 261 -3 261 -166 95 c-91 52 -190 109 -219 125 -30 17 -52 34 -51 39 3 9 424 256 437 255 3 0 59 -31 125 -69z"></path>
      </g>
    </svg>
    <span class="ml-2">Login to OpenPanel</span>
  </a>
  <span class="mx-2">or</span>
  <a href="/user/new">Create Another</a>
`;


    function submitForm(form, debug) {
        const csrfToken = document.querySelector('meta[name="csrf-token"]').getAttribute('content');
        var formData = new FormData(form);
        var statusMessageDiv = document.getElementById("statusMessage");
        statusMessageDiv.innerText = ""; // Clear previous messages

        const scrollinglogAreaDiv = document.getElementById("scrollinglogAreaDiv");
        scrollinglogAreaDiv.classList.remove("hidden");
        scrollinglogAreaDiv.classList.add("active"); // for spinner
        scrollinglogAreaDiv.classList.add("block"); // display

        const createUserForm = document.getElementById("userForm");
        createUserForm.classList.add("hidden"); // Hide form on submit

    const username = document.getElementById('admin_username').value;
    const password = document.getElementById('admin_password').value;



document.getElementById('title').textContent = `Creating user..`;
document.getElementById('subtitle').textContent = `Creating user: '${username}', please wait for the process to finish.`;



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
                            scrollinglogAreaDiv.scrollTop = scrollinglogAreaDiv.scrollHeight; // Auto-scroll
                            
                            // Check if the message contains specific info to trigger toast notifications
                            if (text.includes("Successfully added user")) {
                                showToast(`Success, user ${username} was created successfully.`, 'success');

document.getElementById('title').textContent = `User created successfully!`;
document.getElementById('subtitle').textContent = `Username: ${username} Password: ${password}`;
helpBtn.parentElement.appendChild(newLink);

                            } else if (text.toLowerCase().includes("error")) {
                                showToast("Error, please check the log below the form.", 'error');
                                createUserForm.classList.remove("hidden");

document.getElementById('title').textContent = `Error creating user!`;
document.getElementById('subtitle').textContent = `Error creating user: '${username}', please check the log below the form.`;

                            }
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

            document.getElementById("scrollinglogAreaDiv").classList.remove("active");
            createUserForm.classList.remove("hidden");

            // Check for messages from server to display in the status message
            if (response.message && response.message.includes("consider purchasing the Enterprise version")) {
                const updatedMessage = response.message.replace(
                    "consider purchasing the Enterprise version",
                    '<a href="https://my.openpanel.com/index.php?rp=/store/openpanel/enterprise-license" target="_blank" rel="noopener">consider purchasing the Enterprise version</a>'
                );
                statusMessageDiv.innerHTML = updatedMessage;
                createUserForm.classList.remove("hidden");
            } else if (response.message && !response.message.includes("Successfully added user")) {
                const toastMessage = `Error, please check the log below the form.`;
                showToast(toastMessage, 'error');
document.getElementById('title').textContent = `Error creating user!`;
document.getElementById('subtitle').textContent = `Error creating user: '${username}', please check the log below the form.`;


                createUserForm.classList.remove("hidden");
            } else if (response.message && response.message.includes("Successfully added user")) {
                showToast(`Success, user ${username} was created successfully.`, 'success');

document.getElementById('title').textContent = `User created successfully!`;
document.getElementById('subtitle').textContent = `Username: ${username} \nPassword: ${password}`;
helpBtn.parentElement.appendChild(newLink);



            }
        }).catch(error => {
            createUserButton.disabled = false;
            createUserButton.innerHTML = 'Create User';
            document.getElementById("scrollinglogAreaDiv").classList.remove("active");
            //const toastMessage = `Error occurred, please try again later.`;
            //showToast(toastMessage, 'error');
        });
    }
});

