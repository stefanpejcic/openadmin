document.addEventListener("DOMContentLoaded", function () {
    var checkboxes = document.querySelectorAll('.form-selectgroup-input');

    function selectedServices() {
        var selectedServices = [];
        checkboxes.forEach(function (checkbox) {
            if (checkbox.checked) {
                selectedServices.push(checkbox.value);
            }
        });
        document.getElementById('selectedServices').value = selectedServices.join(',');
    }

    checkboxes.forEach(function (checkbox) {
        checkbox.addEventListener('change', selectedServices);
    });

    selectedServices();
});
