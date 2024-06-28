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
