$(document).ready(function() {

    // Perform initial fetch of license key on page load
    verifyLicense();

$('#license-form').on('submit', function(event) {
    event.preventDefault(); // Prevent default form submission
    var key = $('#license-key').val();
    var alertContainer = $('#license-alerts');
    
    const saveButton = document.querySelector('#save_license_btn');

    saveButton.disabled = true;
    saveButton.innerHTML = '<span class="spinner-grow spinner-grow-sm" role="status" aria-hidden="true"></span>&nbsp; Validating...';

    // Clear any previous alerts
    alertContainer.empty();

    $.ajax({
        url: '/license/key',
        method: 'POST',
        contentType: 'application/json',
        headers: {
            'X-CSRFToken': $('meta[name="csrf-token"]').attr('content')
        },
        data: JSON.stringify({ key: key }),
        success: function(response) {
            if (response.response === "License is invalid") {
                saveButton.disabled = false;
                saveButton.innerHTML = 'Save key';

                alertContainer.append(`
                    <div class="alert alert-danger alert-dismissible fade show" role="alert">
                      <div class="d-flex">
                        <div>
                          <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="icon alert-icon"><path stroke="none" d="M0 0h24v24H0z" fill="none"></path><path d="M3 12a9 9 0 1 0 18 0a9 9 0 0 0 -18 0"></path><path d="M12 8v4"></path><path d="M12 16h.01"></path></svg>
                        </div>
                        <div>
                          License key is invalid. Please verify it on your <a href="https://my.openpanel.com/clientarea.php?action=products" target="_blank">my.openpanel.com</a> account.
                        </div>
                      </div>
                        <button type="button" class="btn-close" data-bs-dismiss="alert" aria-label="Close"></button>
                    </div>
                `);
            } else {
                saveButton.disabled = false;
                saveButton.innerHTML = 'Save key';
                alertContainer.append(`
                    <div class="alert alert-success alert-dismissible fade show" role="alert">
                      <div class="d-flex">
                        <div>
                          <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="icon alert-icon"><path stroke="none" d="M0 0h24v24H0z" fill="none"></path><path d="M5 12l5 5l10 -10"></path></svg>
                        </div>
                        <div>
                          License key saved successfully! <a href="/service/restart/admin">Click here to restart OpenAdmin</a> interface and apply Enterprise features.
                        </div>
                      </div>
                        <button type="button" class="btn-close" data-bs-dismiss="alert" aria-label="Close"></button>
                    </div>
                `);
                fetchLicenseKey(); // After saving key, fetch it again to trigger verify process
            }
        },
        error: function(xhr, status, error) {
            saveButton.disabled = false;
            saveButton.innerHTML = 'Save key';
            console.error('Error saving license key:', error);

            let responseMessage = 'License key is added. Refresh the page to display new features.';
            if (xhr.responseJSON && xhr.responseJSON.error === "License key validation failed") {
                responseMessage = 'License key validation failed. Please check your key and try again.';
            }

            alertContainer.append(`
                <div class="alert alert-warning alert-dismissible fade show" role="alert">
                      <div class="d-flex">
                        <div>
                          <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="icon alert-icon"><path stroke="none" d="M0 0h24v24H0z" fill="none"></path><path d="M12 9v4"></path><path d="M10.363 3.591l-8.106 13.534a1.914 1.914 0 0 0 1.636 2.871h16.214a1.914 1.914 0 0 0 1.636 -2.87l-8.106 -13.536a1.914 1.914 0 0 0 -3.274 0z"></path><path d="M12 16h.01"></path></svg>
                        </div>
                        <div>
                          ${responseMessage}
                        </div>
                      </div>
                    <button type="button" class="btn-close" data-bs-dismiss="alert" aria-label="Close"></button>
                </div>
            `); 
        }
    });
});




});




    // Function to perform AJAX GET request to /license/info
    function verifyLicense() {
        var key = $('#license-key').val(); // Get the value of the license key input field
        const verifyButton = document.querySelector('.btn-verify');
        
        $.ajax({
            url: '/license/info',
            method: 'GET',
            headers: {
                'X-CSRFToken': $('meta[name="csrf-token"]').attr('content')  // Add CSRF token to the headers
            },
            contentType: 'application/json',
            success: function(response) {
                try {
                    var infoString = response.info;
                    var infoObject = JSON.parse(infoString); // Parse the 'info' string into a JavaScript object

                    var itemsHtml = Object.keys(infoObject).map(function(key) {
                        return "<li><strong>" + key + ":</strong> " + infoObject[key] + "</li>";
                    }).join("");

                    var listHtml = "<ul>" + itemsHtml + "</ul>";

                    $('#license_data').html(listHtml);
                    $('#license_info').removeClass('d-none');
                    verifyButton.disabled = false;
                    verifyButton.innerHTML = '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="icon icon-tabler icons-tabler-outline icon-tabler-reload"><path stroke="none" d="M0 0h24v24H0z" fill="none"></path><path d="M19.933 13.041a8 8 0 1 1 -9.925 -8.788c3.899 -1 7.935 1.007 9.425 4.747"></path><path d="M20 4v5h-5"></path></svg> Re-Verify ';
                } catch (e) {
                    verifyButton.disabled = false;
                    verifyButton.innerHTML = '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="icon icon-tabler icons-tabler-outline icon-tabler-reload"><path stroke="none" d="M0 0h24v24H0z" fill="none"></path><path d="M19.933 13.041a8 8 0 1 1 -9.925 -8.788c3.899 -1 7.935 1.007 9.425 4.747"></path><path d="M20 4v5h-5"></path></svg> Re-Verify ';
                    // If JSON parsing fails, handle it here
                    $('#license_data').html("<p>" + response.info + "</p>");
                    $('#license_info').removeClass('d-none');
                }

            },
            error: function(xhr, status, error) {
                console.error('Error verifying license:', error);
                verifyButton.disabled = false;
                verifyButton.innerHTML = '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="icon icon-tabler icons-tabler-outline icon-tabler-reload"><path stroke="none" d="M0 0h24v24H0z" fill="none"></path><path d="M19.933 13.041a8 8 0 1 1 -9.925 -8.788c3.899 -1 7.935 1.007 9.425 4.747"></path><path d="M20 4v5h-5"></path></svg> Re-Verify ';
            }
        });
    }




    document.addEventListener("DOMContentLoaded", function() {
        const verifyButton = document.querySelector('.btn-verify');
        const downgradeButton = document.querySelector('.btn-downgrade');

        // Add click event listener for Verify button
        verifyButton.addEventListener("click", function(event) {
            event.preventDefault(); // Prevent default link behavior
            verifyButton.disabled = true;
            verifyButton.innerHTML = '<span class="spinner-grow spinner-grow-sm" role="status" aria-hidden="true"></span>&nbsp; Checking...';
            verifyLicense(); // Call the verifyLicense function
        });

        // Add click event listener for Downgrade button
        downgradeButton.addEventListener("click", function(event) {
            event.preventDefault(); // Prevent default link behavior
            downgradeButton.disabled = true;
            downgradeButton.innerHTML = '<span class="spinner-grow spinner-grow-sm" role="status" aria-hidden="true"></span>&nbsp; Working...';


            // Perform a DELETE request to the server
            fetch('/license/delete', {
                method: 'DELETE',
                headers: {
                    'X-CSRFToken': $('meta[name="csrf-token"]').attr('content')  // Add CSRF token to the headers
                }
            })
            .then(function(response) {
                downgradeButton.disabled = false;
                downgradeButton.innerHTML = '<svg  xmlns="http://www.w3.org/2000/svg"  width="24"  height="24"  viewBox="0 0 24 24"  fill="none"  stroke="currentColor"  stroke-width="2"  stroke-linecap="round"  stroke-linejoin="round"  class="icon icon-tabler icons-tabler-outline icon-tabler-trending-down-2"><path stroke="none" d="M0 0h24v24H0z" fill="none"/><path d="M3 6h5l7 10h6" /><path d="M18 19l3 -3l-3 -3" /></svg> Downgrade';
                // Reload the page after 5 seconds
                setTimeout(function() {
                    location.reload();
                }, 5000); // 5000 milliseconds = 5 seconds
            })
            .catch(function(error) {
                console.error('Error downgrading license:', error);
              downgradeButton.disabled = false;
              downgradeButton.innerHTML = '<svg  xmlns="http://www.w3.org/2000/svg"  width="24"  height="24"  viewBox="0 0 24 24"  fill="none"  stroke="currentColor"  stroke-width="2"  stroke-linecap="round"  stroke-linejoin="round"  class="icon icon-tabler icons-tabler-outline icon-tabler-trending-down-2"><path stroke="none" d="M0 0h24v24H0z" fill="none"/><path d="M3 6h5l7 10h6" /><path d="M18 19l3 -3l-3 -3" /></svg> Downgrade';
            });
        });
    });
