document.addEventListener('DOMContentLoaded', function () {
    const storedUsername = localStorage.getItem('rememberedUsername');
    const usernameInput = document.querySelector('input[name="username"]');
    // Autofill the username if it's stored
    if (storedUsername) {
        usernameInput.value = storedUsername;
    }

    document.querySelector('form').addEventListener('submit', function (event) {
        const rememberMeCheckbox = document.getElementById('rememberMe');
        if (rememberMeCheckbox.checked) {
            localStorage.setItem('rememberedUsername', usernameInput.value);
        } else {
            localStorage.removeItem('rememberedUsername');
        }
    });
});
