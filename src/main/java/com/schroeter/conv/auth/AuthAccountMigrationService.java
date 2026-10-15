package com.schroeter.conv.auth;

import java.nio.file.Files;
import java.nio.file.Path;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.ResultSet;
import java.sql.Statement;
import java.time.LocalDateTime;
import java.util.ArrayList;
import java.util.List;

import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.boot.context.event.ApplicationReadyEvent;
import org.springframework.context.event.EventListener;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

@Service
public class AuthAccountMigrationService {

    private static final Logger log = LoggerFactory.getLogger(AuthAccountMigrationService.class);

    private final String datasourceUrl;
    private final String legacyDbBasePath;
    private final UserAccountRepository userAccountRepository;

    public AuthAccountMigrationService(
            @Value("${spring.datasource.url}") String datasourceUrl,
            @Value("${app.auth.legacy-db-base-path:data/convdb}") String legacyDbBasePath,
            UserAccountRepository userAccountRepository) {
        this.datasourceUrl = datasourceUrl;
        this.legacyDbBasePath = legacyDbBasePath;
        this.userAccountRepository = userAccountRepository;
    }

    @EventListener(ApplicationReadyEvent.class)
    @Transactional
    public void migrateLegacyAccountsIfNeeded() {
        String currentBase = extractH2BasePath(datasourceUrl);
        if (currentBase == null) {
            return;
        }

        Path currentBasePath = Path.of(currentBase).toAbsolutePath().normalize();
        Path legacyBase = Path.of(legacyDbBasePath).toAbsolutePath().normalize();
        log.info("Auth DB path: {}", currentBasePath);

        if (currentBasePath.equals(legacyBase)) {
            return;
        }

        Path legacyDbFile = Path.of(legacyBase + ".mv.db");
        if (!Files.exists(legacyDbFile)) {
            return;
        }

        long currentUsers = userAccountRepository.count();
        if (currentUsers > 0) {
            return;
        }

        List<LegacyUser> legacyUsers = loadLegacyUsers(legacyBase);
        if (legacyUsers.isEmpty()) {
            return;
        }

        for (LegacyUser legacyUser : legacyUsers) {
            if (userAccountRepository.findByUsername(legacyUser.username()).isPresent()) {
                continue;
            }
            UserAccount account = new UserAccount(
                    legacyUser.username(),
                    legacyUser.passwordHash(),
                    legacyUser.role(),
                    legacyUser.createdAt());
            account.markLogin(legacyUser.lastLoginAt());
            userAccountRepository.save(account);
        }

        log.warn("Migrated {} legacy user account(s) from {}", legacyUsers.size(), legacyBase);
    }

    private List<LegacyUser> loadLegacyUsers(Path legacyBase) {
        List<LegacyUser> users = new ArrayList<>();
        String legacyUrl = "jdbc:h2:file:" + legacyBase + ";MODE=MySQL;AUTO_SERVER=TRUE";
        try (Connection conn = DriverManager.getConnection(legacyUrl, "sa", "");
                Statement stmt = conn.createStatement()) {
            if (!tableExists(conn, "USER_ACCOUNTS")) {
                return users;
            }
            try (ResultSet rs = stmt.executeQuery(
                    "SELECT username, password_hash, role, created_at, last_login_at FROM user_accounts")) {
                while (rs.next()) {
                    String username = rs.getString("username");
                    String passwordHash = rs.getString("password_hash");
                    String role = rs.getString("role");
                    LocalDateTime createdAt = rs.getTimestamp("created_at").toLocalDateTime();
                    var lastLoginTs = rs.getTimestamp("last_login_at");
                    LocalDateTime lastLoginAt = lastLoginTs == null ? null : lastLoginTs.toLocalDateTime();
                    if (username == null || username.isBlank() || passwordHash == null || passwordHash.isBlank()) {
                        continue;
                    }
                    users.add(new LegacyUser(
                            username.trim().toLowerCase(),
                            passwordHash,
                            (role == null || role.isBlank()) ? "USER" : role,
                            createdAt,
                            lastLoginAt));
                }
            }
        } catch (Exception e) {
            log.warn("Legacy auth migration skipped: {}", e.getMessage());
        }
        return users;
    }

    private boolean tableExists(Connection conn, String tableNameUpper) {
        try (Statement stmt = conn.createStatement();
                ResultSet rs = stmt.executeQuery(
                        "SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_NAME='" + tableNameUpper + "'")) {
            if (!rs.next()) {
                return false;
            }
            return rs.getInt(1) > 0;
        } catch (Exception e) {
            return false;
        }
    }

    private static String extractH2BasePath(String url) {
        if (url == null) {
            return null;
        }
        String normalized = url.trim();
        if (!normalized.startsWith("jdbc:h2:file:")) {
            return null;
        }
        String withoutPrefix = normalized.substring("jdbc:h2:file:".length());
        int sep = withoutPrefix.indexOf(';');
        if (sep >= 0) {
            return withoutPrefix.substring(0, sep);
        }
        return withoutPrefix;
    }

    private record LegacyUser(
            String username,
            String passwordHash,
            String role,
            LocalDateTime createdAt,
            LocalDateTime lastLoginAt) {
    }
}
