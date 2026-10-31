function updateUserActivityTable() {
    $.ajax({
        url: '/json/combined_activity?cluster=all',
        type: 'GET',
        dataType: 'json',
        success: function (data) {
            $('#activity-list').empty();

            if (data.combined_logs.length > 0) {
                $('#shouldbehidden').hide();
                
                data.combined_logs.forEach(function (log) {
                    var parts = log.split(' ');
                    var date = parts.slice(0, 3).join(' ');
                    var ip = parts[3];
                    var userAndActivity = parts.slice(4).join(' ');
                    var usernameMatch = parts[4] === 'Administrator' ? parts[5].match(/(\w+)/) : userAndActivity.match(/User (\w+)/);
                    var username = usernameMatch ? usernameMatch[1] : '';

                    var logMoment = moment(date, 'YYYY-MM-DD HH:mm:ss');
                    var now = moment();

                    var daysDiff = now.clone().startOf('day')
                        .diff(logMoment.clone().startOf('day'), 'days');

                    var dateLabel;
                    if (daysDiff === 0) {
                        dateLabel = 'Today';
                    } else if (daysDiff === 1) {
                        dateLabel = 'Yesterday';
                    } else if (daysDiff > 1 && daysDiff <= 6) {
                        dateLabel = `${daysDiff} days ago`;
                    } else {
                        dateLabel = logMoment.format('D.M.Y');
                    }

                    var formattedDate = `${dateLabel} ${logMoment.format('H:mm:ss')}`;

                    var isOnline = Math.abs(now.diff(moment(date, 'YYYY-MM-DD HH:mm:ss'), 'minutes')) <= 90;

                    var avatarClass = parts[4] === 'Administrator' 
                        ? 'bg-black dark:bg-black text-white' 
                        : isOnline 
                            ? 'bg-sky-500 dark:bg-sky-500 text-white' 
                            : 'bg-gray-50 dark:bg-gray-800 text-gray-700 dark:text-white';

                    var hreflink = parts[4] === 'Administrator' 
                        ? '/administrators' 
                        : `/users/${username}`;

                    var avatarContent;

                    if (parts[4] === 'Administrator') {
                        avatarContent = '<svg xmlns="http://www.w3.org/2000/svg" class="remixicon" width="24" height="24" viewBox="0 0 24 24" fill="currentColor"><path d="M2.00488 19H22.0049V21H2.00488V19ZM2.00488 5L7.00488 8.5L12.0049 2L17.0049 8.5L22.0049 5V17H2.00488V5ZM4.00488 8.84131V15H20.0049V8.84131L16.5854 11.2349L12.0049 5.28024L7.42435 11.2349L4.00488 8.84131Z"/></svg>';
                    } else {
                        avatarContent = username && username.length > 0
                            ? username[0].toUpperCase()
                            : '?';
                    }

                    var listItem = `
                        <li class="flex flex-col items-center justify-between gap-4 pl-1 py-4 sm:flex-row sm:py-3 hover:bg-gray-50 hover:dark:bg-gray-900 truncate">
                            <div class="flex w-full items-center gap-4">
                                <a href="${hreflink}" title="Click to view User">
                                    <span class="inline-flex size-9 items-center justify-center rounded-full ${avatarClass} p-1.5 text-xs font-medium ring-1 ring-gray-300 dark:ring-gray-700" aria-hidden="true">
                                        ${avatarContent}
                                    </span>
                                </a>
                                <div class="truncate">
                                    <p class="text-sm font-medium text-gray-900 dark:text-gray-50"><a href="/users/${username}#activity" title="Click to view the Activity Log">${ip}</a></p>
                                    <p class="text-xs text-gray-600 dark:text-gray-400 truncate">${userAndActivity.replace(/user (\w+)/i, 'User <strong>$1</strong>')}</p>
                                </div>
                            </div>
                            <div class="flex w-full items-center gap-3 sm:w-fit">
                                <div class="text-xs text-gray-600 dark:text-gray-400">
                                    <span title="${logMoment.format('D.M.Y H:mm:ss')}" class="cursor-help">
                                        ${formattedDate}
                                    </span>
                                </div>
                            </div>
                        </li>`;

                    $('#activity-list').append(listItem);
                });
            } else {
                $('#shouldbehidden').show();
            }
        },
        error: function (error) {
            console.error('Error fetching user activity:', error);
        }
    });
}

// --- Init ---
$(function () {
    updateUserActivityTable();
});
