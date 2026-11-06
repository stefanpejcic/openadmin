document.addEventListener("DOMContentLoaded", function () {
    // the info box is only there once a key is saved
    if (document.querySelector('#license_data')) verifyLicense();

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
                                    interface and apply Enterprise features (may take up to 10sec).</p>
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
                        }, 10000);
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
        if (!infoContainer) return;
        if (!response.info) {
            infoContainer.textContent = response.error || 'No license information available.';
            verifyButton.innerHTML = 'Re-Verify';
            verifyButton.disabled = false;
            return;
        }

        try {
            let infoObject = JSON.parse(response.info);
            let listHtml = `<ul class='list-disc pl-5 text-gray-700'>` +
                           Object.keys(infoObject).map(key => `<li><strong>${key}:</strong> ${infoObject[key]}</li>`).join('') +
                           `</ul>`;
            infoContainer.innerHTML = listHtml;
            const dueWarning = licenseDueWarning(infoObject['Next Due Date'], new Date());
            if (dueWarning) {
                const p = document.createElement('p');
                p.className = 'mt-3 text-sm text-amber-600 dark:text-amber-500';
                p.textContent = dueWarning;
                infoContainer.appendChild(p);
            }
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

// warns from 3 days before the next due date, the license gets suspended on that date
function licenseDueWarning(dueDate, now) {
    const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(dueDate || '');
    // free and lifetime licenses have no due date, the api sends 0000-00-00 for those
    if (!m || +m[1] === 0) return '';
    const due = new Date(+m[1], +m[2] - 1, +m[3]);
    const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
    const days = Math.round((due - today) / 86400000);
    if (days > 3) return '';
    const what = 'Renew the license, or remove the license key to downgrade to the Community edition.';
    if (days < 0) return `The license was due on ${m[0]} and gets suspended. ${what}`;
    if (days === 0) return `The license is due today (${m[0]}) and gets suspended once that date passes. ${what}`;
    return `The license is due in ${days} day${days === 1 ? '' : 's'} (${m[0]}) and gets suspended on that date. ${what}`;
}
