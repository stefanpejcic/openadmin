document.addEventListener("DOMContentLoaded", function () {
    verifyLicense();

    document.querySelector('#license-form').addEventListener('submit', function (event) {
        event.preventDefault();
        var key = document.querySelector('#license-key').value;
        var alertContainer = document.querySelector('#license-alerts');
        const saveButton = document.querySelector('#save_license_btn');

        saveButton.disabled = true;
        saveButton.innerHTML = '<span class="animate-spin h-5 w-5 border-4 border-gray-300 border-t-gray-600 rounded-full inline-block"></span>&nbsp; Validating...';
        alertContainer.innerHTML = '';

        fetch('/license/key', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'X-CSRFToken': document.querySelector('meta[name="csrf-token"]').getAttribute('content')
            },
            body: JSON.stringify({ key: key })
        })
        .then(response => response.json())
        .then(response => {
            saveButton.disabled = false;
            saveButton.innerHTML = 'Save key';
            let alertHtml = '';

            if (response.response === "License is invalid" || response.error === "License key validation failed") {
                alertHtml = `<div class="p-4 sm:p-6 lg:p-8">
                                <div class='bg-red-100 border-l-4 border-red-500 text-red-700 p-4 rounded-md'>
                                    <p><strong>License key is invalid.</strong> Please verify it on your 
                                    <a href='https://my.openpanel.com/clientarea.php?action=products' class='text-blue-500 underline' target='_blank'>my.openpanel.com</a> account.</p>
                                </div>
                             </div>`;
            } else {
                alertHtml = `<div class="p-4 sm:p-6 lg:p-8">
                                <div class='bg-green-100 border-l-4 border-green-500 text-green-700 p-4 rounded-md'>
                                    <p>License key saved successfully! 
                                    <a href="#" id="restartOpenAdmin" class='text-blue-500 underline'>Click here to restart OpenAdmin</a> 
                                    interface and apply Enterprise features.</p>
                                </div>
                            </div>`;

                setTimeout(verifyLicense, 0);
            }

            alertContainer.innerHTML = alertHtml;

            const restartLink = document.getElementById("restartOpenAdmin");
            if (restartLink) {
                restartLink.replaceWith(restartLink.cloneNode(true));
                document.getElementById("restartOpenAdmin").addEventListener("click", function (event) {
                    event.preventDefault();
                    fetch('/service/restart/admin', {
                        method: 'POST',
                        headers: {
                            'Content-Type': 'application/json',
                            'X-CSRFToken': document.querySelector('meta[name="csrf-token"]').getAttribute('content')
                        }
                    })
                    .then(() => {
                        setTimeout(() => {
                            window.location.href = "/license";
                        }, 5000);
                    })
                    .catch(error => console.error("Error restarting OpenAdmin:", error));
                });
            }
        })
        .catch(error => {
            console.error('Error saving license key:', error);
            saveButton.disabled = false;
            saveButton.innerHTML = 'Save key';
            alertContainer.innerHTML = `<div class="p-4 sm:p-6 lg:p-8">
                                            <div class='bg-yellow-100 border-l-4 border-yellow-500 text-yellow-700 p-4 rounded-md'>
                                                <p>License key validation failed. Please check your key and try again.</p>
                                            </div>
                                        </div>`;
        });
    });

    document.querySelector('.btn-verify').addEventListener('click', function (event) {
        event.preventDefault();
        this.disabled = true;
        this.innerHTML = '<span class="animate-spin h-5 w-5 border-4 border-gray-300 border-t-gray-600 rounded-full inline-block"></span>&nbsp; Checking...';
        verifyLicense();
    });

    document.querySelector('.btn-downgrade').addEventListener('click', function (event) {
        event.preventDefault();
        const downgradeButton = this;
        downgradeButton.disabled = true;
        downgradeButton.innerHTML = '<span class="animate-spin h-5 w-5 border-4 border-gray-300 border-t-gray-600 rounded-full inline-block"></span>&nbsp; Working...';

        fetch('/license/delete', {
            method: 'DELETE',
            headers: {
                'X-CSRFToken': document.querySelector('meta[name="csrf-token"]').getAttribute('content')
            }
        })
        .then(() => {
            downgradeButton.disabled = false;
            downgradeButton.innerHTML = 'Downgrade';
            setTimeout(() => location.reload(), 5000);
        })
        .catch(error => {
            console.error('Error downgrading license:', error);
            downgradeButton.disabled = false;
            downgradeButton.innerHTML = 'Downgrade';
        });
    });
});

function verifyLicense() {
    fetch('/license/info', {
        method: 'GET',
        headers: {
            'X-CSRFToken': document.querySelector('meta[name="csrf-token"]').getAttribute('content')
        }
    })
    .then(response => response.json())
    .then(response => {
        let infoContainer = document.querySelector('#license_data');
        let verifyButton = document.querySelector('.btn-verify');

        try {
            let infoObject = JSON.parse(response.info);
            let listHtml = `<ul class='list-disc pl-5 text-gray-700'>` +
                           Object.keys(infoObject).map(key => `<li><strong>${key}:</strong> ${infoObject[key]}</li>`).join('') +
                           `</ul>`;
            infoContainer.innerHTML = listHtml;
        } catch (e) {
            infoContainer.innerHTML = `<p class="text-red-600">${response.info}</p>`;
        }

        document.querySelector('#license_info').classList.remove('hidden');
        verifyButton.innerHTML = 'Re-Verify';
        verifyButton.disabled = false;
    })
    .catch(error => {
        console.error('Error verifying license:', error);
        document.querySelector('.btn-verify').innerHTML = 'Re-Verify';
        document.querySelector('.btn-verify').disabled = false;
    });
}
