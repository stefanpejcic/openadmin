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


    // Set custom image name
  const customRadioButton = document.querySelector('input[name="docker_image"][value="custom"]');
  if (customRadioButton !== null && customRadioButton.checked) {
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
    xhr.setRequestHeader("X-CSRFToken", $('meta[name="csrf-token"]').attr('content'));
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
    headers: {
      "X-CSRFToken": $('meta[name="csrf-token"]').attr('content')  // Add CSRF token to the headers
    }
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
      	valueNames: [ 'sort-id', 'sort-name', 'sort-image', 'sort-du', 'sort-storage', 'sort-domains', 'sort-sites', 'sort-emails', 'sort-ftp', 'sort-db', 'sort-cpu', 'sort-ram', 'sort-port',
      		{ attr: 'data-inodes', name: 'sort-inode' },
      	]
      });
      })






// EDIT PLAN

$(document).ready(function() {
    // Attach click event listener to "Edit" buttons
    $('.btn-edit').click(function() {
        // Get the row corresponding to the clicked "Edit" button
        var row = $(this).closest('tr');
        
        // Extract data from the row
        var id = row.data('id');
        var name = row.data('name');
        var description = row.data('description');
        var dockerImage = row.data('image');
        var diskLimitReal = row.find('.sort-du').text();
        var diskLimit = (diskLimitReal.trim() === '∞') ? '0' : diskLimitReal.replace(' GB', '');
        var storageFileInodesText = row.find('.sort-storage-inodes').text();
        var domainsLimitReal = row.find('.sort-domains').text();
        var domainsLimit = (domainsLimitReal.trim() === '∞') ? '0' : domainsLimitReal;
        var websitesLimitReal = row.find('.sort-sites').text();
        var websitesLimit = (websitesLimitReal.trim() === '∞') ? '0' : websitesLimitReal;

        var ftpLimitReal = row.find('.sort-ftp').text();
        var ftpLimit = (ftpLimitReal.trim() === '∞') ? '0' : ftpLimitReal;
        var emailsLimitReal = row.find('.sort-emails').text();
        var emailsLimit = (emailsLimitReal.trim() === '∞') ? '0' : emailsLimitReal;



        var dbLimitReal = row.find('.sort-db').text();
        var dbLimit = (dbLimitReal.trim() === '∞') ? '0' : dbLimitReal;
        var cpuReal = row.find('.sort-cpu').text();
        var cpu = (cpuReal.trim() === '∞') ? '0' : cpuReal;
        var ramReal = row.find('.sort-ram').text();
        var ram = (ramReal.trim() === '∞') ? '0' : ramReal;
        var portSpeedReal = row.find('.sort-port').text();
        var portSpeed = (portSpeedReal.trim() === '∞') ? '10000' : portSpeedReal.replace(' mbits', '');

        // Populate modal fields with extracted data
        $('#edit_id').val(id);
        $('#edit_name').val(name);
        $('#edit_description').val(description);
        $('#edit_docker_image').val(dockerImage);
        $('#edit_disk_limit').val(diskLimit);
        $('#edit_storage_file_inodes').val(storageFileInodesText);
        $('#edit_domains_limit').val(domainsLimit);
        $('#edit_websites_limit').val(websitesLimit);
        $('#edit_email_limit').val(emailsLimit);
        $('#edit_ftp_limit').val(ftpLimit);
        $('#edit_db_limit').val(dbLimit);
        $('#edit_cpu').val(cpu);
        $('#edit_ram').val(ram);
        $('#edit_port_speed').val(portSpeed);
    });
});





// Function to handle edit plan form submission
function submitForm() {
    // Serialize form data
    const formData = new FormData(document.getElementById('EditplanForm'));
    const url = '/plan/edit';

    // Make POST request
    fetch(url, {
        method: 'POST',
        headers: {
          "X-CSRFToken": $('meta[name="csrf-token"]').attr('content')  // Add CSRF token to the headers
        },
        body: formData
    })
    .then(response => {
        if (response.ok) {
            // Extract success message
            response.json().then(data => {
                const success = data.success || false;
                let responseMessage = success ? data.response.message : '';
                responseMessage = responseMessage.replace(/\n/g, '<br>');
                const modalBody = document.querySelector('#modal-edit-plan .modal-body');
                //const modalHeader = document.querySelector('#modal-edit-plan .modal-header');
                const modalFooter = document.querySelector('#modal-edit-plan .modal-footer');
                const modalContent = document.querySelector('#modal-edit-plan .modal-content');

    // Extract filename if "opencli_plan_apply_" is present
    const filenameMatch = responseMessage.match(/opencli_plan_apply_\d{8}_\d{6}\.log/);
    if (filenameMatch) {
        const filename = filenameMatch[0];
        const preTag = document.createElement('pre');
        preTag.style.height = '30em';

        const successParagraph = document.createElement('p');
        successParagraph.innerHTML = responseMessage;
        successParagraph.innerHTML = successParagraph.innerHTML.replace(/(tail -f \/tmp\/opencli_plan_apply_\d{8}_\d{6}\.log)/g, '<code>$1</code>');


        // Function to fetch data from /plan/apply/<filename> and update pre tag
        const fetchDataAndUpdatePreTag = () => {
            fetch(`/plan/apply/${filename}`)
            .then(response => response.text())
            .then(data => {
                preTag.textContent = data;
                modalFooter.innerHTML = '';
                modalBody.innerHTML = '';
                modalBody.appendChild(successParagraph);
                modalBody.appendChild(preTag); 

                if (data.includes('COMPLETED')) {
                    clearInterval(intervalId);
                    setTimeout(() => {
                        window.location.reload();
                    }, 1500);
                }
            })
            .catch(error => {
                console.error('Error fetching data:', error);
            });
        };
        
        // Fetch data initially
        fetchDataAndUpdatePreTag();
        
        // Refresh every 1 second
        const intervalId = setInterval(fetchDataAndUpdatePreTag, 1000);
        } else {
                // If filenameMatch is not found, display success alert
                const alertDiv = document.createElement('div');
                if (responseMessage.includes('ERROR')) {
                alertDiv.className = 'alert alert-danger';
                } else {
                alertDiv.className = 'alert alert-success';
                }

                alertDiv.innerHTML = responseMessage;
                modalContent.insertBefore(alertDiv, modalContent.children[1]);

                // Hide alerts after 5 seconds
                setTimeout(() => {
                    alertDiv.style.display = 'none';
                }, 5000);


            }


            });
        } else {
            // Handle error response
            response.json().then(data => {
                const errorMessage = data.error || 'An error occurred while processing your request.';

                // Create a new div for alert
                const alertDiv = document.createElement('div');
                alertDiv.className = 'alert alert-danger';
                alertDiv.innerHTML = errorMessage;

                // Insert the new div after modal header
                modalContent.insertBefore(alertDiv, modalContent.children[1]); // Assuming the second child is modal-body
                
                // Hide alerts after 5 seconds
                setTimeout(() => {
                    alertDiv.style.display = 'none';
                }, 5000);
            });
        }
    })
    .catch(error => {
        console.error('Error:', error);
        const modalContent = document.querySelector('#modal-edit-plan .modal-content'); // Targeting modal content

        // Create a new div for alert
        const alertDiv = document.createElement('div');
        alertDiv.className = 'alert alert-danger';
        alertDiv.innerHTML = 'An error occurred while processing your request.';

        // Insert the new div after modal header
        modalContent.insertBefore(alertDiv, modalContent.children[1]); // Assuming the second child is modal-body
        
        // Hide alerts after 5 seconds
        setTimeout(() => {
            alertDiv.style.display = 'none';
        }, 5000);
    });
}






$(document).ready(function() {
    $('#SavePlanButton').click(function() {
        submitForm();
    });
});




document.addEventListener('DOMContentLoaded', function() {
  // Function to check URL hash and add class to the matching row
  function applyClassBasedOnHash() {
    // Get the hash from the URL (e.g., #ubuntu_apache_mysql)
    const hash = window.location.hash.substring(1); // Remove the # symbol

    // If there's a hash value, find the matching <tr> by data-name attribute
    if (hash) {
      // Remove 'this-user-row' class from all rows first (optional, depending on your logic)
      document.querySelectorAll('tr.this-user-row').forEach(function(row) {
        row.classList.remove('this-user-row');
      });

      // Find the row with the matching data-name
      const matchingRow = document.querySelector(`tr[data-name="${hash}"]`);

      // If the matching row is found, add the class
      if (matchingRow) {
        matchingRow.classList.add('this-user-row');
      }
    }
  }

  // Run the function on page load (if there's already a hash in the URL)
  applyClassBasedOnHash();

  // Listen for hash changes in the URL
  window.addEventListener('hashchange', applyClassBasedOnHash);
});






