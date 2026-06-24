using Microsoft.AspNetCore.Mvc;
using Acme.Services;
using Acme.Models;
using Sys = System.Text;

namespace Acme.Controllers;

[ApiController]
[Route("api/users")]
public class UserController : ControllerBase
{
    private readonly UserService service;

    public UserController(UserService service)
    {
        this.service = service;
    }

    [HttpGet("{id}")]
    public User GetUser(int id) => service.GetUser(id);

    [HttpPost]
    public void CreateUser(User user) => service.Register(user);
}
