(function() {
    var canvas = document.getElementById('cycleDurationChart');
    if (!canvas) return;
    var labels = JSON.parse(canvas.getAttribute('data-labels') || '[]');
    var values = JSON.parse(canvas.getAttribute('data-values') || '[]');
    if (labels.length === 0) return;
    var ctx = canvas.getContext('2d');
    new Chart(ctx, {
        type: 'line',
        data: {
            labels: labels,
            datasets: [{
                label: 'Duration (seconds)',
                data: values,
                borderColor: 'rgb(99, 132, 255)',
                backgroundColor: 'rgba(99, 132, 255, 0.1)',
                fill: true,
                tension: 0.3
            }]
        },
        options: {
            responsive: true,
            scales: {
                x: { title: { display: true, text: 'Cycle', color: '#999' }, ticks: { color: '#999' }, grid: { color: '#333' } },
                y: { title: { display: true, text: 'Seconds', color: '#999' }, ticks: { color: '#999' }, grid: { color: '#333' }, beginAtZero: true }
            },
            plugins: { legend: { labels: { color: '#ccc' } } }
        }
    });
})();
