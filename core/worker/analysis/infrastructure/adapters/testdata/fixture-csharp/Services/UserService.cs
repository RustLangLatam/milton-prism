using Acme.Data;
using Acme.Models;

namespace Acme.Services;

public class UserService
{
    private readonly UserRepository repository;

    public UserService(UserRepository repository)
    {
        this.repository = repository;
    }

    public User GetUser(int id) => repository.Find(id);

    public void Register(User user) => repository.Save(user);
}
