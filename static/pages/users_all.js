// INDICATE DEDI IP ADDRESS IN USERS TABLE
function updatePublicIP(username, ip) {
    const ipElements = document.querySelectorAll(`[data-username="${username}"] .public-ip`);
    ipElements.forEach(element => {
        // Check if the current IP is the same as the new one
        const currentIP = element.textContent.trim();
        if (currentIP === ip) return; // Skip if no change needed

        // Clear the content before updating to avoid duplicate spans
        element.textContent = ip;

        // Create the span with classes
        const span = document.createElement('span');
        span.className = "flex size-4 shrink-0 items-center justify-center rounded-full bg-blue-500 ring-2 ring-white dark:bg-blue-500 dark:ring-[#090E1A]";

        // Create the SVG element inside the span
        span.innerHTML = `
            <svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" width="24" height="24" fill="currentColor" aria-hidden="true" class="size-2.5 text-white dark:text-white">
                <path d="M11.9998 17L6.12197 20.5902L7.72007 13.8906L2.48926 9.40983L9.35479 8.85942L11.9998 2.5L14.6449 8.85942L21.5104 9.40983L16.2796 13.8906L17.8777 20.5902L11.9998 17Z"></path>
            </svg>
        `;

        // Append the span after the updated IP
        element.appendChild(span);
    });
}



// DISPLAY USER ONLINE STATUS
fetch('/json/user_activity_status')
  .then(response => response.json())
  .then(data => {
    const headerCells = document.querySelectorAll('thead th');
    let onlineColIndex = -1;
    headerCells.forEach((th, idx) => {
      const span = th.querySelector('span');
      if (span && span.textContent.trim().toLowerCase() === 'online') {
        onlineColIndex = idx;
      }
    });
    if (onlineColIndex === -1) {
      console.warn('No "online" column found');
      return;
    }

    document.querySelectorAll('tbody tr').forEach(row => {
      const usernameCell = row.querySelector('td a[href^="/users/"]');
      if (!usernameCell) return;

      const username = usernameCell.getAttribute('href').split('/').pop();
      const isActive = data[username] === 'active';

      const onlineCell = row.children[onlineColIndex];
      if (!onlineCell) return;

      onlineCell.textContent = '';

      if (isActive) {
        const dot = document.createElement('span');
        dot.classList.add('user-status-dot');
        dot.textContent = '●';
        dot.style.color = 'green';
        dot.style.fontSize = '0.9rem';
        dot.style.verticalAlign = 'middle';
        onlineCell.appendChild(dot);
      }
    });
  })
  .catch(error => console.error('Error fetching user activity status:', error));
