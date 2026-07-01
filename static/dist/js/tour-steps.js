/* OpenPanel UI product tour - step definitions, grouped by page in walkthrough order.
   Add new pages by appending more entries with the matching `path`. */
window.TOUR_HOOKS = {
    openProfileMenu: function () {
        var btn = document.getElementById('user-btn-info');
        var menu = document.getElementById('popup-menu');
        if (btn && menu && menu.classList.contains('hidden')) btn.click();
    },
    closeProfileMenu: function () {
        var btn = document.getElementById('user-btn-info');
        var menu = document.getElementById('popup-menu');
        if (btn && menu && !menu.classList.contains('hidden')) btn.click();
    }
};

window.TOUR_STEPS = [
    {
        path: '/dashboard',
        element: '#tour-resource-usage',
        title: 'Resource Usage',
        description: 'Updated periodically. Hover over it to see details like CPU cores or partition names. Click it to jump to Process Manager, sorted by that resource so you can see exactly what’s using it.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/dashboard',
        element: '#tour-quick-summary',
        title: 'Quick Summary',
        description: 'A live summary of your current Accounts, Domains, Packages and more.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/dashboard',
        element: '#tour-user-activity',
        title: 'User Activity',
        description: 'The combined activity log for all users. OpenPanel records 260+ actions across the panel for full transparency.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/dashboard',
        element: '#tour-latest-news',
        title: 'Latest News',
        description: 'Articles from the OpenPanel blog — mostly new major features, or tips and tricks. Click Dismiss to hide this widget for good.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/dashboard',
        element: '#tour-system-info',
        title: 'System Information',
        description: 'General info about your server: uptime, hostname, OS and more.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/dashboard',
        element: '#searchInput',
        title: 'Search',
        description: 'Quickly find any feature, or search for a user. Click a username to view their profile, or the OpenPanel icon next to it to impersonate them.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/dashboard',
        element: '#tour-notifications-link',
        title: 'Notifications',
        description: 'Shows a tooltip when system alerts are available. Notifications are customizable from Settings → Notifications.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/dashboard',
        element: '#tour-recent-menu',
        title: 'Recent',
        description: 'Shows the last 5 pages you visited in the UI. Pin any of them to keep it on top.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/dashboard',
        element: '#tour-main-menu',
        title: 'Main Menu',
        description: 'Accounts, Hosting Plans, Domains, Settings and the rest of OpenPanel’s features live here.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/dashboard',
        element: '#popup-menu',
        title: 'Profile Menu',
        description: 'Click your profile at the bottom of the sidebar to see the OpenPanel version, switch Dark theme, or sign out.',
        side: 'right',
        align: 'start',
        beforeShow: 'openProfileMenu',
        beforeHide: 'closeProfileMenu'
    },
    {
        path: '/dashboard',
        element: '#tour-report-bug-link',
        title: 'Found a Bug?',
        description: 'Help us improve by reporting any bugs directly on GitHub to our developers.',
        side: 'top',
        align: 'end'
    },
    {
        path: '/license',
        element: '#license-key',
        title: 'License Key',
        description: 'Add your OpenPanel Enterprise license key to unlock additional features: DNS clustering, MariaDB, unlimited user accounts, FTP, Emails and more.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/license',
        element: '#save_license_btn',
        title: 'Save Key',
        description: 'Add the license key, then restart the admin panel to upgrade.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/license',
        element: '#tour-generate-report-btn',
        title: 'Generate Report',
        description: 'When you need support, click Generate Report.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/settings/general',
        element: '#domain',
        title: 'Domain',
        description: 'Set the domain or IP address used for accessing both the user panel and the admin panel.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/general',
        element: '#ports',
        title: 'Ports',
        description: 'Custom ports can also be set here. If you’re using Cloudflare, make sure to use Full SSL mode on non-standard HTTPS ports to avoid errors.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/general',
        element: '#devmode',
        title: 'Debugging (Dev Mode)',
        description: 'Dev Mode logs detailed info for both the user and admin panels. This should stay off in production and only be used when debugging something.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/open-panel',
        element: '#brand',
        title: 'Brand / Logo',
        description: 'Configure branding by setting a custom logo - link to an image from your website.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/open-panel',
        element: '#nameservers',
        title: 'Nameservers',
        description: 'Configure nameservers if you’ll be using DNS on this server. If not - for example if you’re using Cloudflare NS - you can skip this.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/open-panel',
        element: '#tour-save-openpanel-btn',
        title: 'Save Changes',
        description: 'Click Save Changes when done.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/notifications',
        element: '#tour-notifications-table',
        title: 'Notifications',
        description: 'View your notifications - these include both system alerts and alerts on selected user/admin panel actions.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/notifications',
        element: '#tour-notification-details',
        title: 'Details',
        description: 'If extra info is available for a notification, it shows up in the Details column.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/notifications',
        element: '#tour-notification-actions',
        title: 'Actions',
        description: 'Click the check icon to dismiss a notification (marks it as read, so it can re-trigger if the issue reoccurs), or the trash icon to delete it.',
        side: 'left',
        align: 'start'
    },
    {
        path: '/notifications',
        element: '#tour-bulk-actions',
        title: 'Bulk Actions',
        description: 'Click Acknowledge All or Delete All to perform the action on every notification at once.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/notifications',
        element: '#tour-notifications-search',
        title: 'Search',
        description: 'Search through your notifications.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/notifications',
        element: '#tour-edit-settings-link',
        title: 'Edit Settings',
        description: 'Click Edit Settings to customize your notification preferences.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/settings/notifications',
        element: '#email',
        title: 'Email',
        description: 'Set your contact info - email or webhook - to receive alerts.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/notifications',
        element: '#webhook_url',
        title: 'Webhook',
        description: 'Alerts can also be sent to a webhook URL instead of (or in addition to) email.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/notifications',
        element: '#services',
        title: 'Services',
        description: 'Get notified when a service is down or not responding.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/notifications',
        element: '#thresholds',
        title: 'Resource Usage',
        description: 'Receive a notification when resource usage exceeds a threshold.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/notifications',
        element: '#server-actions',
        title: 'Server Actions',
        description: 'Receive a notification when a certain action is detected on the server.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/notifications',
        element: '#website-traffic',
        title: 'Website Traffic',
        description: 'Detects unusual traffic indicating DDoS attacks.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/notifications',
        element: '#user-actions',
        title: 'User Actions',
        description: 'Get notified whenever an action occurs in the admin or user panels.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/notifications',
        element: '#smtp',
        title: 'SMTP',
        description: 'All notifications default to no-reply@openpanel.org. Set up your own SMTP server here to send email notifications to administrators and OpenPanel user accounts instead.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/notifications',
        element: '#tour-save-changes-btn',
        title: 'Save Changes',
        description: 'Click Save Changes in the top right whenever you want to save.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/plans',
        element: '#exiting_users',
        title: 'Plans',
        description: 'View existing plans. Plans set what resources are available to users on that plan, and what feature set is used for them.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/plans',
        element: '#tour-plans-edit',
        title: 'Edit',
        description: 'Click Edit to edit an existing plan.',
        side: 'left',
        align: 'start'
    },
    {
        path: '/plans',
        element: '#tour-create-plan-btn',
        title: 'Create New',
        description: 'Click Create New to add a new plan.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/features',
        element: '#manage',
        title: 'Feature Sets',
        description: 'Manage feature sets. Feature sets dictate which features in the user panel are available to users on each hosting plan. Select an existing one here to edit it, or create a new one above.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/features/default',
        element: '#tour-features-grid',
        title: 'Features',
        description: 'Turn each feature on or off individually for this feature set.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/features/default',
        element: '#tour-features-module-col',
        title: 'Module',
        description: 'Modules need to be active for features to work - this column shows which module each feature depends on.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/features/default',
        element: '#tour-bulk-features',
        title: 'Bulk Actions',
        description: 'Use Enable All or Disable All to toggle every feature at once.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/features/default',
        element: '#tour-features-search',
        title: 'Search',
        description: 'Search for a specific feature.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/features/default',
        element: '#tour-save-features-btn',
        title: 'Save Changes',
        description: 'Make sure to click Save Changes when done. The user panel needs to be restarted to show the new features to users on that plan.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/settings/modules',
        element: '#tour-modules-grid',
        title: 'Modules',
        description: 'Enable modules (options) for the user panel - for example, enable Emails to give users access to mail, or Varnish to let them control Varnish caching. Toggle each one on or off individually.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/settings/modules',
        element: '#tour-bulk-modules',
        title: 'Bulk Actions',
        description: 'Use Enable All or Disable All to toggle every module at once.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/settings/modules',
        element: '#tour-modules-filters',
        title: 'Search & Sort',
        description: 'Search for a module, or sort based on its tag: Enterprise, Beta, Community and so on.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/settings/modules',
        element: '#tour-save-modules-btn',
        title: 'Save Changes',
        description: 'Make sure to click Save Changes when done. The user panel needs to be restarted to start modules.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/settings/defaults',
        element: '#webserver',
        title: 'Webserver',
        description: 'Define the default webserver to be used for new user accounts. This can be overwritten on account creation.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/defaults',
        element: '#mysql',
        title: 'Database',
        description: 'Define the default MySQL type to be used for new user accounts. This can be overwritten on account creation.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/defaults',
        element: '#varnish',
        title: 'Varnish',
        description: 'Turn Varnish on or off by default for new user accounts. This can be overwritten on account creation.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/defaults',
        element: '#default-php',
        title: 'Default PHP',
        description: 'Define the default PHP version to be used for new user accounts.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/defaults',
        element: '#autostart-services',
        title: 'Autostart Services',
        description: 'Select which services to autostart for the user when you create them. Only start those the user will actually need - OpenPanel UI pages already autostart services when needed, for example MySQL when its page is accessed, or cron when a new one is added.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/defaults',
        element: '#services',
        title: 'Services',
        description: 'Set CPU/RAM limits for the user services, their versions, and other environment variables.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/defaults',
        element: '#tour-save-defaults-btn',
        title: 'Save',
        description: 'Click Save when done.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/users',
        element: '#exiting_users',
        title: 'User Accounts',
        description: 'View current OpenPanel accounts. You can search, and sort by status or by column.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/users',
        element: '#dropdownToggleButton',
        title: 'Show Columns',
        description: 'Show Columns lets you view all possible options.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/users',
        element: '#tour-create-user-btn',
        title: 'Create New',
        description: 'Click Create New to create a new account.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/user/new',
        element: '#admin_username',
        title: 'Account Details',
        description: 'Set a username, email and password for the new account.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/user/new',
        element: '#tour-user-plan',
        title: 'Plan',
        description: 'Choose a plan.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/user/new',
        element: '#tour-user-advanced',
        title: 'Advanced',
        description: 'Optionally click Advanced to overwrite the webserver, MySQL type and Varnish. On Enterprise, you can also set a reseller for this user.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/user/new',
        element: '#CreateUserButton',
        title: 'Create User',
        description: 'Click Create User and wait for it to finish.',
        side: 'top',
        align: 'end'
    },
    {
        path: '/resellers',
        element: '#exiting_users',
        title: 'Resellers',
        description: 'Table shows all reseller users, their usage, and options to manage them.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/resellers',
        element: '#tour-create-reseller-btn',
        title: 'Create New',
        description: 'Click Create New to create a new reseller.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/administrators',
        element: '#exiting_users',
        title: 'Administrators',
        description: 'Shows admin users and resellers. On Enterprise, you can create multiple accounts - useful to share access with support teams and to create resellers.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/user/import',
        element: '#path',
        title: 'Backup File',
        description: 'wget a backup file of a cPanel or CyberPanel full account into the /home directory, then select the file here.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/user/import',
        element: '#tour-import-type',
        title: 'Backup Type',
        description: 'Select the type - cPanel or CyberPanel.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/user/import',
        element: '#tour-import-plan',
        title: 'Plan',
        description: 'Set a plan for the imported account.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/user/import',
        element: '#ImportUserButton',
        title: 'Import',
        description: 'Click Import to start.',
        side: 'top',
        align: 'end'
    },
    {
        path: '/user/import',
        element: '#tour-import-view-logs',
        title: 'View Logs',
        description: 'Click View Logs to view past or running import processes.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/domains',
        element: '#domains',
        title: 'Domains',
        description: 'View domains in the table - their status, PHP version, SSL, WAF and owner.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/domains',
        element: '#tour-domains-actions',
        title: 'Actions',
        description: 'Edit DNS, suspend/unsuspend, manage SSL, edit the vhost/Caddyfile, or delete a domain.',
        side: 'left',
        align: 'start'
    },
    {
        path: '/domains',
        element: '#tour-add-domain-btn',
        title: 'Add Domain',
        description: 'Click Add Domain to add a domain to a user.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/domains',
        element: '#dropdownToggleButton',
        title: 'Show Columns',
        description: 'Show Columns also reveals all available info, like HSTS status and Domain ID.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/domains/dns',
        element: '#toAddActive',
        title: 'DNS Zone Editor',
        description: 'Select a domain, then edit its DNS records in the text area and save.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/domains/dns-cluster',
        element: '#tour-dns-cluster-toggle',
        title: 'DNS Cluster',
        description: 'Enable clustering, then add other slave BIND9 servers to be used as slave DNS. Follow the docs on how to set up slave servers.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/domains/zone-templates',
        element: '#ipv4',
        title: 'Zone Templates',
        description: 'Edit the default DNS templates (IPv4 and IPv6) used for new user domains.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/domains/file-templates',
        element: '#default-page',
        title: 'Default Page',
        description: 'Edit the default page shown for domains the user added that don’t have any website files yet.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/domains/file-templates',
        element: '#suspended-website',
        title: 'Suspended Website',
        description: 'Shown on a website when the user suspends that domain.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/domains/file-templates',
        element: '#suspended-user',
        title: 'Suspended User',
        description: 'Shown on all domains belonging to a user when that user is suspended.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/domains/file-templates',
        element: '#apache',
        title: 'VHost Templates',
        description: 'VHost templates for Apache, Nginx, OpenLiteSpeed and more, used for new user domains.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/php',
        element: '#options',
        title: 'Available Options',
        description: 'Configure which options are available to users from the PHP Options page.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/php',
        element: '#files',
        title: 'Default PHP.INI Files',
        description: 'Edit the default php.ini files used for each PHP version.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/php',
        element: '#tour-first-ini-toggle',
        title: 'Edit a Version',
        description: 'Click on the name to open that version’s section and reveal a text area with its raw php.ini content.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/settings/php',
        element: '#tour-first-ini-buttons',
        title: 'Restore Default / Save',
        description: 'Use Restore Default to reset this php.ini to its default content, or Save to apply your changes.',
        side: 'left',
        align: 'start'
    },
    {
        path: '/services',
        element: '#exiting_users',
        title: 'Services',
        description: 'View system services in the table - status, version and port. If monitoring is enabled, the service will be auto-restarted on failure. Use the Start/Stop/Restart actions to control each one.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/services',
        element: '#tour-edit-services-btn',
        title: 'Edit Services',
        description: 'Click Edit Services to edit which services are monitored - for example to add your new service.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/services/ftp',
        element: '#tour-ftp-status',
        title: 'FTP',
        description: 'Install the FTP service if needed, then view your FTP accounts here once it’s running.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/services/ftp',
        element: '#tour-ftp-config-tab',
        title: 'Configuration',
        description: 'On the Configuration tab, edit the vsftpd.conf file - for example to set a custom domain.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/services/limits',
        element: '#tour-first-service-limit',
        title: 'Service Limits',
        description: 'Configure CPU, RAM and environment variables for system containers. Stop and start the service for new values to apply. Defaults are shown below each field if you want to restore them.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/services/limits',
        element: '#tour-save-limits-btn',
        title: 'Save',
        description: 'Click Save when done.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/services/logs',
        element: '#log-select',
        title: 'Select a Log',
        description: 'Select the log name in the top right.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/services/logs',
        element: '#lines-select',
        title: 'Lines',
        description: 'Choose how many lines to show.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/services/logs',
        element: '#log-content',
        title: 'Log Content',
        description: 'Once the log opens, its content is shown here.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/services/logs',
        element: '#tour-log-actions',
        title: 'Actions',
        description: 'Download or delete the log from here.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/services/logs',
        element: '#tour-log-settings-link',
        title: 'Settings',
        description: 'Set paths to custom logs that you want to view from this page.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/security/firewall',
        element: '#myiframe',
        title: 'Sentinel Firewall',
        description: 'Sentinel Firewall - a fork of ConfigServer & Firewall - is available here, with options to open ports, whitelist IPs and more.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/security/waf',
        element: '#enable',
        title: 'WAF',
        description: 'Enable/disable Coraza WAF on the server and for all existing domains.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/security/waf',
        element: '#sets',
        title: 'Rule Sets',
        description: 'View OWASP CoreRuleSet sets and manage them. Manage opens the Rules page.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/security/waf/rules',
        element: '#waf_sets',
        title: 'WAF Rules',
        description: 'Lists rule sets and the rules within them, shows their status, and lets you view a set’s rules or disable it.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/security/imunify',
        element: '#myiframe',
        title: 'ImunifyAV',
        description: 'If ImunifyAV is installed, it shows here. View scan results and initiate malware scans for user files.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/security/imunify',
        element: '#tour-imunify-install',
        title: 'ImunifyAV',
        description: 'If ImunifyAV isn’t installed, you get the option to install it here. It supports AMD CPUs only.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/security/basic_auth',
        element: '#enable',
        title: 'Basic Authentication',
        description: 'Enable basic access authentication as an additional security measure for OpenAdmin.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/security/basic_auth',
        element: '#logins',
        title: 'Logins',
        description: 'Set a username and password.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/security/basic_auth',
        element: '#tour-save-basic-auth-btn',
        title: 'Save',
        description: 'Click Save to enable it. On the next login to the admin panel, these credentials will be required before the login page is even shown.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/security/2fa',
        element: '#tour-2fa',
        title: 'Two-Factor Authentication',
        description: 'Protect your OpenAdmin account with an additional one-time code from an authenticator app. This sets 2FA per account and requires it on the next login for that account.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/security/passkeys',
        element: '#add-passkey-btn',
        title: 'Passkeys',
        description: 'Sign in to OpenAdmin without a password using a fingerprint, face scan, device PIN, or security key. Add a passkey here - it requires a custom domain to be set to work.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/security/passkeys',
        element: '#tour-passkeys-needs-domain',
        title: 'Passkeys',
        description: 'Sign in to OpenAdmin without a password using a fingerprint, face scan, device PIN, or security key. It requires a custom domain to be set to work.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/security/blacklist-useragents',
        element: '#enable',
        title: 'Blacklist User Agents',
        description: 'Blacklist known malicious bots from accessing the user panel. Enable it here.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/security/blacklist-useragents',
        element: '#blacklist_useragents_list',
        title: 'User Agents',
        description: 'Set the user agents to block.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/security/blacklist-useragents',
        element: '#tour-save-blacklist-ua-btn',
        title: 'Save',
        description: 'Click Save. A restart of the user panel is needed to apply.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/security/disable-admin',
        element: '#tour-disable-admin-confirm',
        title: 'Disable OpenAdmin',
        description: 'As an advanced security measure, you can disable access to the OpenAdmin interface. Once disabled, you can’t access it from the browser and need to manually enable it on the terminal with `opencli admin on`.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/server/resource-usage',
        element: '#tour-tab-load',
        title: 'Load',
        description: 'Click Load to show the server load.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/server/resource-usage',
        element: '#tour-tab-ram',
        title: 'RAM',
        description: 'Click RAM to show RAM and swap, with the option to drop cache and clear swap.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/server/resource-usage',
        element: '#tour-tab-cpu',
        title: 'CPU',
        description: 'Click CPU to view CPU usage per core.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/server/resource-usage',
        element: '#tour-tab-disk',
        title: 'Disk',
        description: 'Click Disk to view disk usage and IO.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/server/resource-usage',
        element: '#tour-tab-network',
        title: 'Network',
        description: 'Click Network to view network IO.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/server/crons',
        element: '#tour-crons-schedule',
        title: 'Schedule',
        description: 'System crons needed for OpenPanel to function. Edit the schedule for each one to tweak how often it runs.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/server/crons',
        element: '#tour-crons-logging',
        title: 'Logging',
        description: 'Turn logging on/off per job to view it in the log later, once it has run.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/terminal',
        element: '#shell',
        title: 'Terminal',
        description: 'Available only to SuperAdmin. Opens a terminal into the server. Choose sh/bash in the top right, then type commands - it’s interactive.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/server/processes',
        element: '#processes_table',
        title: 'Process Manager',
        description: 'View current system processes, sort the table, trace or kill a process, and view full commands.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/server/root-password',
        element: '#password',
        title: 'Root Password',
        description: 'Change the SSH password for the root user. Insert the new password in the field and click the button.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/server/ssh',
        element: '#port',
        title: 'SSH Access',
        description: 'Edit the sshd config for the server. Set the port, password/key auth, and use Advanced to edit the file directly.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/server/reboot',
        element: '#reboot_type',
        title: 'Server Reboot',
        description: 'Initiate a server reboot. Select the type and click the button to confirm.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/server/timezone',
        element: '#timezone',
        title: 'Timezone',
        description: 'Set the timezone used on the server. Useful to keep system logs in the same zone as yours, and new users inherit it.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/server/migrate',
        element: '#tour-migrate-host',
        title: 'Server Migration',
        description: 'Insert SSH logins for a remote server that should have a clean OS install. This will transfer the installation - with all user accounts and data - to the new server.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/settings/updates',
        element: '#version',
        title: 'Versions',
        description: 'View the currently installed OpenPanel version, the latest available version and its changelog, and set your update preferences. We recommend enabling both minor and major updates for security patches.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/updates',
        element: '#logs',
        title: 'Update Logs',
        description: 'View logs from previous updates.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/updates',
        element: '#rollback',
        title: 'Rollback',
        description: 'Roll back to a previous version if anything went wrong.',
        side: 'right',
        align: 'start'
    },
    {
        path: '/settings/locales',
        element: '#tour-locales-table',
        title: 'Locales',
        description: 'View all available translations for the OpenPanel UI.',
        side: 'top',
        align: 'start'
    },
    {
        path: '/settings/locales',
        element: '#tour-install-action',
        title: 'Install',
        description: 'Click Install to install a locale for your users. Once installed, the user panel needs to be restarted to apply it - then it becomes available to users who have the locale feature enabled on their plan.',
        side: 'bottom',
        align: 'end'
    },
    {
        path: '/settings/locales',
        element: '#tour-default-action',
        title: 'Set as Default',
        description: 'Click Set as Default to make an installed locale the default for new users.',
        side: 'bottom',
        align: 'start'
    },
    {
        path: '/settings/custom-code',
        element: '#tour-custom-code',
        title: 'Custom Code',
        description: 'Add custom CSS and JS to customize the user panel, custom code, set your API keys, a list of domains/usernames to forbid, and custom code to run after every update or on startup.',
        side: 'top',
        align: 'start'
    }
];
