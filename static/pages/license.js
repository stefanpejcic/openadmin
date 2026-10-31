document.addEventListener("DOMContentLoaded", function () {
    // the info box is only there once a key is saved
    if (document.querySelector('#license_data')) verifyLicense();

    document.querySelector('#license-form').addEventListener('submit', function (event) {
        event.preventDefault();
        var key = document.querySelector('#license-key').value;
        var alertContainer = document.querySelector('#license-alerts');
        const saveButton = document.querySelector('#save_license_btn');

        saveButton.disabled = true;
        saveButton.innerHTML = "<svg class='size-4 animate-spin' aria-hidden='true' xmlns='http://www.w3.org/2000/svg' fill='none' viewBox='0 0 24 24'><circle cx='12' cy='12' r='9' stroke='currentColor' stroke-opacity='.25' stroke-width='3'></circle><path d='M21 12a9 9 0 0 0-9-9' stroke='currentColor' stroke-width='3' stroke-linecap='round'></path></svg>Validating…";
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
                alertHtml = `<div class="px-4 pt-6 sm:px-6 lg:px-8">
                                <div class='flex gap-3 rounded-md bg-red-50 p-4 text-sm text-red-900 dark:bg-red-950/70 dark:text-red-500' role='alert'>
                                    <svg class='remixicon size-5 shrink-0 text-red-500' aria-hidden='true' xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='currentColor'><path d='M12 22C6.47715 22 2 17.5228 2 12C2 6.47715 6.47715 2 12 2C17.5228 2 22 6.47715 22 12C22 17.5228 17.5228 22 12 22ZM12 10.5858L9.17157 7.75736L7.75736 9.17157L10.5858 12L7.75736 14.8284L9.17157 16.2426L12 13.4142L14.8284 16.2426L16.2426 14.8284L13.4142 12L16.2426 9.17157L14.8284 7.75736L12 10.5858Z'/></svg>
                                    <p><strong class='font-medium'>License key is invalid.</strong> Verify it in your <a href='https://my.openpanel.com/clientarea.php?action=products' class='text-red-900 underline underline-offset-4 dark:text-red-500' target='_blank' rel='noopener'>my.openpanel.com</a> account.</p>
                                </div>
                             </div>`;
            } else {
                alertHtml = `<div class="px-4 pt-6 sm:px-6 lg:px-8">
                                <div class='flex gap-3 rounded-md bg-emerald-50 p-4 text-sm text-emerald-900 dark:bg-emerald-950/70 dark:text-emerald-500' role='status'>
                                    <svg class='remixicon size-5 shrink-0 text-emerald-500' aria-hidden='true' xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='currentColor'><path d='M12 22C6.47715 22 2 17.5228 2 12C2 6.47715 6.47715 2 12 2C17.5228 2 22 6.47715 22 12C22 17.5228 17.5228 22 12 22ZM11.0026 16L18.0737 8.92893L16.6595 7.51472L11.0026 13.1716L8.17421 10.3431L6.75999 11.7574L11.0026 16Z'/></svg>
                                    <p>License key saved. <a href='#' id='restartOpenAdmin' class='text-emerald-900 underline underline-offset-4 dark:text-emerald-500'>Restart OpenAdmin</a> to apply Enterprise features, it can take up to 10 seconds.</p>
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
            alertContainer.innerHTML = `<div class="px-4 pt-6 sm:px-6 lg:px-8">
                                <div class='flex gap-3 rounded-md bg-yellow-50 p-4 text-sm text-yellow-900 dark:bg-yellow-950/70 dark:text-yellow-500' role='status'>
                                    <svg class='remixicon size-5 shrink-0 text-yellow-500' aria-hidden='true' xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='currentColor'><path d='M12 22C6.47715 22 2 17.5228 2 12C2 6.47715 6.47715 2 12 2C17.5228 2 22 6.47715 22 12C22 17.5228 17.5228 22 12 22ZM11 15V17H13V15H11ZM11 7V13H13V7H11Z'/></svg>
                                    <p>License key validation failed. Check your key and try again.</p>
                                </div>
                             </div>`;
        });
    });

    document.querySelector('.btn-verify').addEventListener('click', function (event) {
        event.preventDefault();
        this.disabled = true;
        this.innerHTML = "<svg class='size-4 animate-spin' aria-hidden='true' xmlns='http://www.w3.org/2000/svg' fill='none' viewBox='0 0 24 24'><circle cx='12' cy='12' r='9' stroke='currentColor' stroke-opacity='.25' stroke-width='3'></circle><path d='M21 12a9 9 0 0 0-9-9' stroke='currentColor' stroke-width='3' stroke-linecap='round'></path></svg>Checking…";
        verifyLicense();
    });

    document.querySelector('.btn-downgrade').addEventListener('click', function (event) {
        event.preventDefault();
        const downgradeButton = this;
        downgradeButton.disabled = true;
        downgradeButton.innerHTML = "<svg class='size-4 animate-spin' aria-hidden='true' xmlns='http://www.w3.org/2000/svg' fill='none' viewBox='0 0 24 24'><circle cx='12' cy='12' r='9' stroke='currentColor' stroke-opacity='.25' stroke-width='3'></circle><path d='M21 12a9 9 0 0 0-9-9' stroke='currentColor' stroke-width='3' stroke-linecap='round'></path></svg>Downgrading…";

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
            verifyButton.innerHTML = 'Re-verify';
            verifyButton.disabled = false;
            return;
        }

        try {
            let infoObject = JSON.parse(response.info);
            let listHtml = `<ul class='space-y-1'>` +
                           Object.keys(infoObject).map(key => `<li><span class='font-medium'>${key}</span> ${infoObject[key]}</li>`).join('') +
                           `</ul>`;
            infoContainer.innerHTML = listHtml;
            const dueWarning = licenseDueWarning(infoObject['Next Due Date'], new Date());
            if (dueWarning) {
                const p = document.createElement('p');
                p.className = 'mt-3 text-xs text-yellow-700 dark:text-yellow-500';
                p.textContent = dueWarning;
                infoContainer.appendChild(p);
            }
        } catch (e) {
            infoContainer.innerHTML = `<p class="text-red-600 dark:text-red-500">${response.info}</p>`;
        }

        document.querySelector('#license_info').classList.remove('hidden');
        verifyButton.innerHTML = 'Re-verify';
        verifyButton.disabled = false;
    })
    .catch(error => {
        console.error('Error verifying license:', error);
        document.querySelector('.btn-verify').innerHTML = 'Re-verify';
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
