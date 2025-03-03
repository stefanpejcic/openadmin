// AUTOLOGIN
async function fetchTokenAndOpenLink(username) {
    const response = await fetch(`/login/token?username=${username}`);

    if (response.ok) {
        const data = await response.json();
        const {
            link: link
        } = data;
        const newTabUrl = `${link}`;
        window.open(newTabUrl, '_blank');
    } else {
        console.error('Failed to fetch token');
    }
}

const loginLinks = document.getElementsByClassName('loginLink');
for (const loginLink of loginLinks) {
    loginLink.addEventListener('click', function(event) {
        event.preventDefault();
        const username = loginLink.getAttribute('user');

        fetchTokenAndOpenLink(username);
    });
}

