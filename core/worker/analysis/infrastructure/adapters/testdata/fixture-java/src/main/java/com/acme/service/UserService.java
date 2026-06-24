package com.acme.service;

import com.acme.model.User;
import com.acme.repo.UserRepository;
import org.springframework.stereotype.Service;

@Service
public class UserService {
    private final UserRepository repository;

    public UserService(UserRepository repository) {
        this.repository = repository;
    }

    public User getUser(int id) {
        return repository.find(id);
    }

    public void register(User user) {
        repository.save(user);
    }
}
