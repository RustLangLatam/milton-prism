global using Acme.Models;

var builder = WebApplication.CreateBuilder(args);
var app = builder.Build();

app.MapGet("/health", () => "ok");
app.MapPost("/echo", (string msg) => msg);

app.Run();
