function updateAllTimestamps() {
    const targets = document.querySelectorAll('.time_ago, .time_readable');

    targets.forEach(el => {
        let rawText = el.getAttribute('x-text') === 'date' ? el.textContent.trim() : el.textContent.trim();

        // Expected format: 2025-04-28-16-00-01
        const parts = rawText.split('-');
        if (parts.length < 6) return;

        const isoDate = `${parts[0]}-${parts[1]}-${parts[2]}T${parts[3]}:${parts[4]}:${parts[5]}`;
        const dateObj = new Date(isoDate);

        if (isNaN(dateObj.getTime())) return;

        // Format full date based on user locale
        const userLocale = navigator.language || 'en-US';
        const fullDate = dateObj.toLocaleString(userLocale, {
            year: 'numeric',
            month: '2-digit',
            day: '2-digit',
            hour: '2-digit',
            minute: '2-digit',
            second: '2-digit',
            hour12: userLocale.includes('en-US') || userLocale.includes('en-CA'),
        });

        el.title = fullDate;

        if (el.classList.contains('time_ago')) {
            // Relative time
            const now = new Date();
            const seconds = Math.floor((now - dateObj) / 1000);
            let display;

            if (seconds < 60) {
                display = `${seconds} ${seconds === 1 ? 'second' : 'seconds'} ago`;
            } else if (seconds < 3600) {
                const minutes = Math.floor(seconds / 60);
                display = `${minutes} ${minutes === 1 ? 'minute' : 'minutes'} ago`;
            } else if (seconds < 86400) {
                const hours = Math.floor(seconds / 3600);
                display = `${hours} ${hours === 1 ? 'hour' : 'hours'} ago`;
            } else {
                const days = Math.floor(seconds / 86400);
                display = `${days} ${days === 1 ? 'day' : 'days'} ago`;
            }

            el.textContent = display;
        } else if (el.classList.contains('time_readable')) {
            // Just use the full date string
            el.textContent = fullDate;
        }
    });
}

window.addEventListener('load', updateAllTimestamps);
