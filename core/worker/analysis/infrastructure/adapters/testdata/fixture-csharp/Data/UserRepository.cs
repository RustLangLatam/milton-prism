using System.Collections.Generic;
using Acme.Models;

namespace Acme.Data;

public class UserRepository
{
    private static int instanceCount;
    private readonly Dictionary<int, User> store = new();

    public User Find(int id) => store.TryGetValue(id, out var u) ? u : null;

    public void Save(User user) => store[user.Id] = user;
}
