  document.addEventListener('DOMContentLoaded', function () {
    // Get all input fields that require validation
    var inputFields = document.querySelectorAll('[data-validate]');

    // Attach an event listener to each input field
    inputFields.forEach(function (inputField) {
      inputField.addEventListener('input', function () {
        // Validate the input on each input event
        validateInput(inputField);
      });
    });

    function validateInput(inputField) {
      // Get the value and validity state of the input
      var value = inputField.value;
      var isValid = inputField.checkValidity();

      // Add or remove classes based on validity
      if (isValid) {
        inputField.classList.remove('is-invalid');
        inputField.classList.add('is-valid');
      } else {
        inputField.classList.remove('is-valid');
        inputField.classList.add('is-invalid');
      }
    }
  });


    // Function to update table rows based on plans search
    function updateTableRows(searchTerm) {
        const rows = document.querySelectorAll('.table tbody tr');
        rows.forEach(row => {
            const description = row.getAttribute('data-description').toLowerCase();
            const name = row.getAttribute('data-name').toLowerCase();
            const id = row.getAttribute('data-id').toLowerCase();
            const image = row.getAttribute('data-image').toLowerCase();

            if (description.includes(searchTerm.toLowerCase()) || id.includes(searchTerm.toLowerCase()) || image.includes(searchTerm.toLowerCase()) || name.includes(searchTerm.toLowerCase())) {
                row.style.display = ''; // Show the row
            } else {
                row.style.display = 'none'; // Hide the row
            }
        });
    }
    // Add event listener to the search input
    const searchInput = document.getElementById('userSearchInput');
    searchInput.addEventListener('input', function () {
        const searchTerm = this.value.trim();
        updateTableRows(searchTerm);
    });




var initialModalBodyContent = document.querySelector(".modal-body").innerHTML;
initialModalTitleContent = document.querySelector(".modal-header").innerHTML;
document.getElementById("CreatePlanButton").addEventListener("click", function () {


    // Set custom iamge name from field
    const customRadioButton = document.querySelector('input[name="docker_image"][value="custom"]');
    if (customRadioButton.checked) {
        const customImageName = document.getElementById("custom_image_name").value;
        customRadioButton.value = customImageName;
    }



    // Disable the button and show loading spinner
    var CreatePlanButton = document.getElementById("CreatePlanButton");
    CreatePlanButton.disabled = true;
    CreatePlanButton.innerHTML = '<span class="spinner-grow spinner-grow-sm" role="status" aria-hidden="true"></span> Creating plan...';

    // Gather form data
    var formData = new FormData(document.getElementById("planForm"));

    // Send AJAX request
    var xhr = new XMLHttpRequest();
    xhr.open("POST", "/plan/new", true);
    xhr.setRequestHeader("Content-Type", "application/x-www-form-urlencoded");

    // Handle response
    xhr.onload = function () {
        // Re-enable the button and hide the spinner
        CreatePlanButton.disabled = false;
        CreatePlanButton.innerHTML = 'Create Plan';

        // Update modal content based on the response
        var modalTitle = document.querySelector(".modal-title");
        var modalBody = document.querySelector(".modal-body");

        if (xhr.status === 200) {
            var response = JSON.parse(xhr.responseText);
            updateModalContent(response.success, response.success ? response.response.message : response.error);
            // Clear form fields
            document.getElementById("planForm").reset();
        } else {
            // Handle errors
            updateModalContent(false, "Error: " + xhr.statusText);
        }
    };

    // Send the request
    xhr.send(new URLSearchParams(formData));

    // Display or update Bootstrap modal content
    function updateModalContent(success, message) {
        var modalTitle = document.querySelector(".modal-title");
        var modalBody = document.querySelector(".modal-body");
        var modalFooter = document.querySelector(".modal-footer");

        modalTitle.innerHTML = success ? "New plan created successfully" : "Error: Creating plan failed!";
        modalBody.innerHTML = "<pre>" + message + "</pre>";
        modalFooter.style.display = "none";
        // Show the modal
        $("#modal-report").modal("show");
    }
});

// Reset modal content on modal close
$('#modal-report').on('hidden.bs.modal', function () {
    var modalTitle = document.querySelector(".modal-title");
    var modalBody = document.querySelector(".modal-body");
    var modalFooter = document.querySelector(".modal-footer");

    // Restore the initial modal content
    modalTitle.innerHTML = initialModalTitleContent;
    modalBody.innerHTML = initialModalBodyContent;
    modalFooter.style.display = "block";
});





document.querySelectorAll('[id^="deleteButton-"]').forEach(button => {
    button.addEventListener("click", function(event) {
        event.preventDefault();
        // Clear the input field and hide the Terminate button when the modal is shown
        document.getElementById("deleteConfirmation").value = "";
        document.getElementById("confirmDelete").style.display = "none";

        var plan_id = this.getAttribute("data-plan-id");
        var plan_name = this.getAttribute("data-plan-name");
        document.getElementById("nameofplan").innerHTML = plan_name;

        // Store the plan ID in the modal for later use
        document.getElementById("confirmDeleteUserModal").setAttribute("data-plan-id", plan_id);
        document.getElementById("confirmDeleteUserModal").setAttribute("data-plan-name", plan_name);
    });
});


document.getElementById("deleteConfirmation").addEventListener("input", function() {
  // Toggle the Terminate button visibility based on the input value
  var confirmationInput = this.value.trim().toUpperCase();
  document.getElementById("confirmDelete").style.display = (confirmationInput === "DELETE") ? "block" : "none";
});

document.getElementById("confirmDelete").addEventListener("click", function() {
  var plan_name = document.getElementById("confirmDeleteUserModal").getAttribute("data-plan-name");

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
      <div class="text-secondary mb-3">Deleting plan, please wait..</div>
      <div class="progress progress-sm">
        <div class="progress-bar progress-bar-indeterminate"></div>
      </div>
    </div>
  `;

  fetch(`/plan/delete/${plan_name}`, {
    method: "POST",
  })
  .then(response => response.json())
  .then(data => {
    if (data.message && data.message.includes("successfully")) {
      window.location.href = "/plans";

    // Hide the modal
    //var modal = new bootstrap.Modal(document.getElementById("confirmDeleteUserModal"));
    //modal.hide();

    } 
    else if (data.error) {
      modalBody.innerHTML = `
        <div class="alert alert-danger" role="alert">
          ${data.error}
        </div>
      `;
      if (data.users && data.users.length > 0) {
        console.log("Affected users:", data.users);
      }
      setTimeout(() => {
        window.location.href = "/plans";
      }, 5000);
    } else {
      modalBody.innerHTML = `
        <div class="alert alert-danger" role="alert">
          ${JSON.stringify(data)}
        </div>
      `;
      setTimeout(() => {
        window.location.href = "/plans";
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
      window.location.href = "/plans";
    }, 5000);
  });

  // Keep the modal displayed in case of an error, do not hide it
});


      document.addEventListener("DOMContentLoaded", function() {
      const list = new List('table-default', {
      	sortClass: 'table-sort',
      	listClass: 'table-tbody',
      	valueNames: [ 'sort-id', 'sort-name', 'sort-image', 'sort-du', 'sort-storage', 'sort-domains', 'sort-sites', 'sort-db', 'sort-cpu', 'sort-ram', 'sort-port',
      		{ attr: 'data-inodes', name: 'sort-inode' },
      	]
      });
      })
