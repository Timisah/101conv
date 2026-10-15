package com.schroeter.conv.auth;

import java.util.Optional;

import org.springframework.data.jpa.repository.JpaRepository;

public interface UserAccountRepository extends JpaRepository<UserAccount, String> {
    Optional<UserAccount> findByUsername(String username);

    boolean existsByRoleIgnoreCase(String role);
}
