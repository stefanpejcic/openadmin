// CLUSTER PAGE
       // Use Ajax to get Docker context data
        $(document).ready(function() {
            $.ajax({
                url: '/json/docker-context',
                type: 'GET',
                dataType: 'json',
                success: function(data) {
                    updateDockerContextsTable(data);
                },
                error: function(error) {
                    console.log(error);
                }
            });
        });

        // Update the Bootstrap table with the received data
        function updateDockerContextsTable(data) {
            var tableBody = $('#docker-contexts-table tbody');
            tableBody.empty(); // Clear the table body

            $.each(data, function(index, context) {
                var row = '<tr>' +
                    '<td>' + context.Name + '</td>' +
                    '<!--td>' + context.Description + '</td-->' +
                    '<td>' + context.DockerEndpoint + '</td>' +
                    '<td>' + (context.Current ? 'Yes' : 'No') + '</td>' +
                    '<!--td>' + context.Error + '</td-->' +
                    '</tr>';
                tableBody.append(row);
            });
        }

