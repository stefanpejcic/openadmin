document.querySelectorAll('[id^="deleteButton-"]').forEach(button => {
    button.addEventListener("click", function(event) {
        event.preventDefault();
        // Clear the input field and hide the Terminate button when the modal is shown
        document.getElementById("deleteConfirmation").value = "";
        document.getElementById("confirmDelete").style.display = "none";

        var image_name = this.getAttribute("data-image-name");
        document.getElementById("nameofimage").innerHTML = image_name;

        // Store the plan ID in the modal for later use
        document.getElementById("confirmDeleteUserModal").setAttribute("data-image-name", image_name);
    });
});

// Check if the element exists
if (document.getElementById("deleteConfirmation")) {
  // Add event listener only if the element exists
  document.getElementById("deleteConfirmation").addEventListener("input", function() {
    // Toggle the Terminate button visibility based on the input value
    var confirmationInput = this.value.trim().toUpperCase();
    document.getElementById("confirmDelete").style.display = (confirmationInput === "DELETE") ? "block" : "none";
  });
}


if (document.getElementById("confirmDelete")) {
  // Add event listener only if the element exists
  document.getElementById("confirmDelete").addEventListener("click", function() {

  var image_name = document.getElementById("confirmDeleteUserModal").getAttribute("data-image-name");

  var deleteModalxClose = document.getElementById("deleteModalxClose");
  var confirmDeleteUserModal = document.getElementById("confirmDeleteUserModal");
  var modalFooter = document.getElementById("confirmDeleteUserModal").getElementsByClassName("modal-footer")[0];

  confirmDeleteUserModal.setAttribute("data-bs-backdrop", "static");
  confirmDeleteUserModal.setAttribute("data-bs-keyboard", "false");
  modalFooter.style.display = "none";
  deleteModalxClose.style.display = "none";

  var modalBody = document.getElementById("confirmDeleteUserModal").getElementsByClassName("modal-body")[0];
  modalBody.innerHTML = `
    <div class="text-center">
      <div class="text-secondary mb-3">Deleting docker image, please wait..</div>
      <div class="progress progress-sm">
        <div class="progress-bar progress-bar-indeterminate"></div>
      </div>
    </div>
  `;

  fetch(`/settings/docker/delete/${image_name}`, {
    method: "POST",
  })
  .then(response => response.json())
  .then(data => {
    if (data.message && data.message.includes("successfully")) {
      window.location.href = "/settings/docker";
    } 
    else if (data.error) {
      modalBody.innerHTML = `
        <div class="alert alert-danger" role="alert">
          ${data.error}
        </div>
      `;
      if (data.users && data.users.length > 0) {
        console.log("Affected plans:", data.users);
      }
      setTimeout(() => {
        window.location.href = "/settings/docker";
      }, 5000);
    } else {
      modalBody.innerHTML = `
        <div class="alert alert-danger" role="alert">
          ${JSON.stringify(data)}
        </div>
      `;
      setTimeout(() => {
        window.location.href = "/settings/docker";
      }, 5000);
    }
  })
  .catch(error => {
    modalBody.innerHTML = `
      <div class="alert alert-danger" role="alert">
        ${error}
      </div>
    `;
    setTimeout(() => {
      window.location.href = "/settings/docker";
    }, 5000);
  });

  // Keep the modal displayed in case of an error, do not hide it
  });
}




        document.getElementById('docker-update-images').addEventListener('click', function(event) {
            event.preventDefault(); // Prevent default link behavior

            // Show message indicating that images are being checked
            document.getElementById('update-message').style.display = 'block';
            document.getElementById('update-message').innerText = 'Checking updates for official Docker images from hub.docker.com\nPlease wait.';
            document.getElementById('update-message').classList.remove('alert-success', 'alert-danger');

            // Make AJAX request to Flask backend
            var xhr = new XMLHttpRequest();
            xhr.open('POST', '/settings/docker', true);
            xhr.setRequestHeader('Content-Type', 'application/json');

            xhr.onload = function() {
                if (xhr.status === 200) {
                    // If successful response received, display the response message
                    var response = JSON.parse(xhr.responseText);
                    document.getElementById('update-message').innerText = response.message;
                    document.getElementById('update-message').classList.add('alert-success');
                } else {
                    // If error response received, display the error message
                    var errorResponse = JSON.parse(xhr.responseText);
                    document.getElementById('update-message').innerText = 'Error: ' + errorResponse.error;
                    document.getElementById('update-message').classList.add('alert-danger');
                }
            };

            xhr.onerror = function() {
                // If request fails, display an error message
                document.getElementById('update-message').innerText = 'Error: Unable to connect to the server.';
                document.getElementById('update-message').classList.add('alert-danger');
            };

            xhr.send(JSON.stringify({ action: 'update' })); //action can be update or configuration
        });
