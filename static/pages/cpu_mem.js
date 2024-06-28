    // Function to fetch Docker container name based on container ID
    async function getContainerName(containerId) {
        try {
            const response = await fetch(`/get_container_name/${containerId}`);
            const data = await response.json();
            return data.username;
        } catch (error) {
            console.error("Error fetching container name:", error);
            return null;
        }
    }

    // Object to store processed container IDs and associated processes
    const processedContainerData = {};

    // Iterate through each process and update the table dynamically
    async function updateTable() {
        {% for process in processes %}
            var cgroup = "{{ process.cgroup }}";
            var memory = "{{ process.usage }}";
            var containerId = "";

            // Check if "docker-" is in the CGroup
            if (cgroup.includes("docker-")) {
                // Extract the container ID
                containerId = cgroup.match(/docker-(.*).scope/)[1];

                // Initialize an array for the container ID if not already done
                if (!processedContainerData[containerId]) {
                    processedContainerData[containerId] = [];
                }

                // Fetch container name and update the table
                const username = await getContainerName(containerId);

                // Add process information to the array
                processedContainerData[containerId].push({
                    pid: "{{ process.pid }}",
                    name: "{{ process.name }}",
                    memory: "{{ process.usage }}",
                    cgroup: "{{ process.cgroup }}",
                    inDocker: "{% if 'docker-' in process.cgroup %}Yes{% else %}No{% endif %}",
                    username: username || 'N/A'
                });
            } else {
                // For processes not in Docker, add directly to the array
                processedContainerData['nonContainer'] = processedContainerData['nonContainer'] || [];
                processedContainerData['nonContainer'].push({
                    pid: "{{ process.pid }}",
                    name: "{{ process.name }}",
                    memory: "{{ process.usage }}",
                    cgroup: "{{ process.cgroup }}",
                    inDocker: 'No',
                    username: ''
                });
            }
        {% endfor %}

        // Sort processes by memory usage (descending order)
        const sortedProcesses = Object.values(processedContainerData).flat().sort((a, b) => b.memory - a.memory);

        // Iterate through the sorted processes and update the table
        sortedProcesses.forEach(process => {
            const inDockerClass = process.inDocker === 'Yes' ? 'bg-dark text-cyan' : '';
            const linkColumn = process.inDocker === 'Yes' ? `<a href="/users/${process.username}">${process.username}</a>` : process.username;

            const pidColumn = `${process.pid} <small>(<a href="/server/pid/${process.pid}/strace">Trace</a>) ( <a href="/server/pid/${process.pid}/kill">Kill</a> )</small>`;


            $("#processTableBody").append(`
                <tr>
                    <th class="${inDockerClass}" scope="row">${pidColumn}</th>
                    <td class="${inDockerClass}">${process.name}</td>
                    <td class="${inDockerClass}">${process.memory}</td>
                    <td class="${inDockerClass}">${linkColumn}</td>
                </tr>
            `);
        });
    }

    // Call the async function to update the table
    updateTable();
