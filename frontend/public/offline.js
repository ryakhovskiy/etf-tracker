// Retry on the offline page: reload the URL the user was trying to open (the worker answers with the real page once online).
document.getElementById("retry").addEventListener("click", function () { window.location.reload(); });
