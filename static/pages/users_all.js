// FILTER USERS TABLE

function updateTableRows(searchTerm) {
    const rows = document.querySelectorAll('.table tbody tr');
    rows.forEach(row => {
        const username = row.getAttribute('data-username').toLowerCase();
        const email = row.getAttribute('data-email').toLowerCase();

        if (username.includes(searchTerm.toLowerCase()) || email.includes(searchTerm.toLowerCase())) {
            row.style.display = '';
        } else {
            row.style.display = 'none';
        }
    });
}
const searchInput = document.getElementById('userSearchInput');

if (searchInput) {
    searchInput.addEventListener('input', function() {
        const searchTerm = this.value.trim();
        updateTableRows(searchTerm);
    });
}




// SHOW DEDI IP ADDRESS IN USERS TABLE

function updatePublicIP(username, ip) {
    const ipElements = document.querySelectorAll(`[data-username="${username}"] .public-ip`);
    ipElements.forEach(element => {
        element.textContent = ip;
    });
}



// DISPLAY USER ONLINE STATUS

fetch('/json/user_activity_status')
  .then(response => response.json())
  .then(data => {
    document.querySelectorAll('tbody tr').forEach(row => {
      const username = row.getAttribute('data-username');
      const status = data[username];

      if (status === 'active') {
        const onlineDiv = document.createElement('div');
        onlineDiv.classList.add('small', 'mt-1');

        const aTag = document.createElement('a');
        aTag.setAttribute('href', `/users/${username}#nav-activity`);
        aTag.setAttribute('style', 'text-decoration: none;');
        onlineDiv.appendChild(aTag);

        const spanBadge = document.createElement('span');
        spanBadge.classList.add('badge', 'bg-green');
        aTag.appendChild(spanBadge);

        const avatarSpan = row.querySelector('span.avatar.me-2');
        if (avatarSpan) {
          avatarSpan.appendChild(onlineDiv);
        }
      }
    });
  })
  .catch(error => console.error('Error fetching user activity status:', error));

