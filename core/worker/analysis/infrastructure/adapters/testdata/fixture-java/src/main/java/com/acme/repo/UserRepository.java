package com.acme.repo;

import com.acme.model.User;
import java.util.HashMap;
import java.util.Map;

public class UserRepository {
    private static int instanceCount;
    private final Map<Integer, User> store = new HashMap<>();

    public User find(int id) {
        return store.get(id);
    }

    public void save(User user) {
        store.put(user.getId(), user);
    }
}
