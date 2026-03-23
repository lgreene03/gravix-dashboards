// Pricing page: monthly/annual billing toggle
(function() {
    'use strict';

    var toggle = document.getElementById('billing-toggle');
    if (!toggle) return;

    var monthlyLabel = document.getElementById('monthly-label');
    var annualLabel = document.getElementById('annual-label');

    toggle.addEventListener('change', function() {
        var isAnnual = toggle.checked;
        var amounts = document.querySelectorAll('.price-amount');

        amounts.forEach(function(el) {
            var monthly = el.getAttribute('data-monthly');
            var annual = el.getAttribute('data-annual');
            if (monthly === null || annual === null) return; // skip "Custom"

            var price = isAnnual ? annual : monthly;
            el.textContent = price === '0' ? '$0' : '$' + price;
        });

        // Update button text for trial plans
        if (monthlyLabel) monthlyLabel.style.fontWeight = isAnnual ? '400' : '600';
        if (annualLabel) annualLabel.style.fontWeight = isAnnual ? '600' : '400';
    });
})();
