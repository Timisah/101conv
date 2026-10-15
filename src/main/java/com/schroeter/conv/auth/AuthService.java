package com.schroeter.conv.auth;

import java.time.Duration;
import java.time.LocalDateTime;
import java.util.Base64;
import java.util.Optional;
import java.util.UUID;
import java.security.NoSuchAlgorithmException;
import java.security.SecureRandom;
import java.security.spec.InvalidKeySpecException;

import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import com.schroeter.conv.config.UserConfigService;

import jakarta.servlet.http.Cookie;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import javax.crypto.SecretKeyFactory;
import javax.crypto.spec.PBEKeySpec;

@Service
public class AuthService {

    private static final Logger log = LoggerFactory.getLogger(AuthService.class);
    private static final String SESSION_COOKIE = "conv_sid";
    private static final int PBKDF2_ITERATIONS = 120_000;
    private static final int PBKDF2_KEY_LENGTH_BITS = 256;
    private static final int SALT_BYTES = 16;
    private static final SecureRandom SECURE_RANDOM = new SecureRandom();

    private final UserAccountRepository userAccountRepository;
    private final AuthSessionRepository authSessionRepository;
    private final UserConfigService userConfigService;

    public AuthService(
            UserAccountRepository userAccountRepository,
            AuthSessionRepository authSessionRepository,
            UserConfigService userConfigService) {
        this.userAccountRepository = userAccountRepository;
        this.authSessionRepository = authSessionRepository;
        this.userConfigService = userConfigService;
    }

    @Transactional
    public UserAccount register(String rawUsername, String rawPassword) {
        if (!userConfigService.allowRegistration()) {
            throw new IllegalArgumentException("Signup is currently disabled.");
        }
        String username = normalizeUsername(rawUsername);
        validatePassword(rawPassword);

        if (userAccountRepository.findByUsername(username).isPresent()) {
            throw new IllegalArgumentException("Username already exists.");
        }

        boolean firstAdmin = !userAccountRepository.existsByRoleIgnoreCase("ADMIN");
        String role = firstAdmin ? "ADMIN" : "USER";
        UserAccount account = new UserAccount(
                username,
                hashPassword(rawPassword),
                role,
                LocalDateTime.now());
        return userAccountRepository.save(account);
    }

    @Transactional
    public Optional<UserAccount> login(String rawUsername, String rawPassword, HttpServletResponse response) {
        String username = normalizeUsername(rawUsername);
        Optional<UserAccount> accountOptional = userAccountRepository.findByUsername(username);
        if (accountOptional.isEmpty()) {
            return Optional.empty();
        }

        UserAccount account = accountOptional.get();
        if (!verifyPassword(rawPassword, account.getPasswordHash())) {
            return Optional.empty();
        }

        LocalDateTime now = LocalDateTime.now();
        account.markLogin(now);

        authSessionRepository.deleteByExpiresAtBefore(now);
        Duration sessionTtl = Duration.ofDays(userConfigService.authSessionDays());

        String sessionId = UUID.randomUUID().toString().replace("-", "") + UUID.randomUUID().toString().replace("-", "");
        AuthSession session = new AuthSession(sessionId, username, now, now.plus(sessionTtl));
        authSessionRepository.save(session);

        Cookie cookie = new Cookie(SESSION_COOKIE, sessionId);
        cookie.setHttpOnly(true);
        cookie.setSecure(false);
        cookie.setPath("/");
        cookie.setMaxAge((int) sessionTtl.toSeconds());
        response.addCookie(cookie);

        return Optional.of(account);
    }

    @Transactional
    public void logout(HttpServletRequest request, HttpServletResponse response) {
        Optional<String> sessionIdOptional = sessionIdFromRequest(request);
        sessionIdOptional.ifPresent(authSessionRepository::deleteById);

        Cookie cookie = new Cookie(SESSION_COOKIE, "");
        cookie.setHttpOnly(true);
        cookie.setSecure(false);
        cookie.setPath("/");
        cookie.setMaxAge(0);
        response.addCookie(cookie);
    }

    @Transactional
    public Optional<String> resolveUserId(HttpServletRequest request) {
        Optional<String> sessionIdOptional = sessionIdFromRequest(request);
        if (sessionIdOptional.isEmpty()) {
            return Optional.empty();
        }

        Optional<AuthSession> sessionOptional = authSessionRepository.findBySessionId(sessionIdOptional.get());
        if (sessionOptional.isEmpty()) {
            return Optional.empty();
        }

        AuthSession session = sessionOptional.get();
        LocalDateTime now = LocalDateTime.now();
        if (session.isExpired(now)) {
            authSessionRepository.delete(session);
            return Optional.empty();
        }

        return Optional.of(session.getUsername());
    }

    public Optional<UserAccount> currentUser(HttpServletRequest request) {
        return resolveUserId(request).flatMap(userAccountRepository::findByUsername);
    }

    @Transactional
    public void changePassword(HttpServletRequest request, String currentPassword, String newPassword) {
        String current = currentPassword == null ? "" : currentPassword;
        String next = newPassword == null ? "" : newPassword;
        validatePassword(next);

        String username = resolveUserId(request)
                .orElseThrow(() -> new IllegalArgumentException("Not authenticated."));

        UserAccount account = userAccountRepository.findByUsername(username)
                .orElseThrow(() -> new IllegalArgumentException("User does not exist."));

        if (!verifyPassword(current, account.getPasswordHash())) {
            throw new IllegalArgumentException("Current password is incorrect.");
        }
        if (current.equals(next)) {
            throw new IllegalArgumentException("New password must be different from the current password.");
        }

        account.updatePasswordHash(hashPassword(next));
        authSessionRepository.deleteByUsername(username);
    }

    @Transactional
    public Optional<String> resetPasswordToTemporary(String rawUsername) {
        String username = normalizeUsername(rawUsername);
        Optional<UserAccount> accountOptional = userAccountRepository.findByUsername(username);
        if (accountOptional.isEmpty()) {
            return Optional.empty();
        }

        String temporaryPassword = generateTemporaryPassword(14);
        UserAccount account = accountOptional.get();
        account.updatePasswordHash(hashPassword(temporaryPassword));
        authSessionRepository.deleteByUsername(username);

        log.warn("Temporary password generated for user '{}': {}", username, temporaryPassword);
        return Optional.of(temporaryPassword);
    }

    private static Optional<String> sessionIdFromRequest(HttpServletRequest request) {
        Cookie[] cookies = request.getCookies();
        if (cookies == null || cookies.length == 0) {
            return Optional.empty();
        }
        for (Cookie cookie : cookies) {
            if (SESSION_COOKIE.equals(cookie.getName())) {
                String value = cookie.getValue();
                if (value != null && !value.isBlank()) {
                    return Optional.of(value);
                }
            }
        }
        return Optional.empty();
    }

    private static String normalizeUsername(String rawUsername) {
        String value = rawUsername == null ? "" : rawUsername.trim().toLowerCase();
        if (value.isBlank()) {
            throw new IllegalArgumentException("Username is required.");
        }
        if (value.length() < 3 || value.length() > 100) {
            throw new IllegalArgumentException("Username must be between 3 and 100 characters.");
        }
        if (!value.matches("[a-z0-9._-]+")) {
            throw new IllegalArgumentException("Username can contain only letters, numbers, dot, underscore, and dash.");
        }
        return value;
    }

    private static void validatePassword(String rawPassword) {
        String value = rawPassword == null ? "" : rawPassword;
        if (value.length() < 8) {
            throw new IllegalArgumentException("Password must be at least 8 characters.");
        }
        if (value.length() > 200) {
            throw new IllegalArgumentException("Password is too long.");
        }
    }

    private static String hashPassword(String rawPassword) {
        byte[] salt = new byte[SALT_BYTES];
        SECURE_RANDOM.nextBytes(salt);
        byte[] hash = pbkdf2(rawPassword.toCharArray(), salt, PBKDF2_ITERATIONS, PBKDF2_KEY_LENGTH_BITS);
        return "pbkdf2$" + PBKDF2_ITERATIONS + "$"
                + Base64.getEncoder().encodeToString(salt) + "$"
                + Base64.getEncoder().encodeToString(hash);
    }

    private static boolean verifyPassword(String rawPassword, String storedHash) {
        if (storedHash == null || storedHash.isBlank()) {
            return false;
        }
        String[] parts = storedHash.split("\\$");
        if (parts.length != 4 || !"pbkdf2".equals(parts[0])) {
            return false;
        }

        try {
            int iterations = Integer.parseInt(parts[1]);
            byte[] salt = Base64.getDecoder().decode(parts[2]);
            byte[] expected = Base64.getDecoder().decode(parts[3]);
            byte[] actual = pbkdf2(rawPassword.toCharArray(), salt, iterations, expected.length * 8);
            return slowEquals(expected, actual);
        } catch (RuntimeException e) {
            return false;
        }
    }

    private static byte[] pbkdf2(char[] password, byte[] salt, int iterations, int keyLengthBits) {
        try {
            PBEKeySpec spec = new PBEKeySpec(password, salt, iterations, keyLengthBits);
            SecretKeyFactory factory = SecretKeyFactory.getInstance("PBKDF2WithHmacSHA256");
            return factory.generateSecret(spec).getEncoded();
        } catch (NoSuchAlgorithmException | InvalidKeySpecException e) {
            throw new IllegalStateException("Could not hash password.", e);
        }
    }

    private static boolean slowEquals(byte[] a, byte[] b) {
        if (a == null || b == null || a.length != b.length) {
            return false;
        }
        int diff = 0;
        for (int i = 0; i < a.length; i++) {
            diff |= a[i] ^ b[i];
        }
        return diff == 0;
    }

    private static String generateTemporaryPassword(int length) {
        String alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789";
        StringBuilder sb = new StringBuilder(length);
        for (int i = 0; i < length; i++) {
            int idx = SECURE_RANDOM.nextInt(alphabet.length());
            sb.append(alphabet.charAt(idx));
        }
        return sb.toString();
    }
}
