package com.schroeter.conv.jobs;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.multipart;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import java.util.UUID;

import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.webmvc.test.autoconfigure.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.mock.web.MockMultipartFile;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.test.web.servlet.MvcResult;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;

import jakarta.servlet.http.Cookie;

@SpringBootTest(classes = com.schroeter.conv.Application.class)
@AutoConfigureMockMvc
class ConversionJobsApiTests {

    private static final Path FIXTURE = createFixture();
    private static final String DATABASE_URL = "jdbc:h2:mem:jobs_" + UUID.randomUUID() + ";DB_CLOSE_DELAY=-1";

    @Autowired MockMvc mvc;
    private final ObjectMapper mapper = new ObjectMapper();

    @DynamicPropertySource
    static void properties(DynamicPropertyRegistry properties) {
        properties.add("spring.datasource.url", () -> DATABASE_URL);
        properties.add("app.data-dir", () -> FIXTURE.resolve("data").toString());
        properties.add("app.user-config-file", () -> FIXTURE.resolve("user.properties").toString());
        properties.add("app.jobs.poll-interval-ms", () -> "25");
        properties.add("app.converter.timeout-seconds", () -> "10");
        properties.add("app.jobs.lease-seconds", () -> "30");
    }

    @Test
    void submitsDurableJobAndScopesStatusAndResultToOwner() throws Exception {
        Cookie alice = register("alice");
        Cookie bob = register("bob");
        mvc.perform(get("/api/v1/profiles")).andExpect(status().isForbidden());
        mvc.perform(get("/api/v1/profiles").cookie(bob)).andExpect(status().isForbidden());
        MvcResult profiles = mvc.perform(get("/api/v1/profiles").cookie(alice)).andExpect(status().isOk()).andReturn();
        JsonNode profileBody = mapper.readTree(profiles.getResponse().getContentAsString());
        assertEquals("101conv-official", profileBody.path("sources").get(0).path("id").asText());
        assertEquals(true, profileBody.path("sources").get(0).path("builtIn").asBoolean());
        mvc.perform(post("/api/v1/profiles/catalogs").cookie(alice)
                        .contentType("application/json").content("{\"url\":\"http://localhost/catalog.json\"}"))
                .andExpect(status().isBadRequest());
        MockMultipartFile file = new MockMultipartFile("file", "input.txt", "text/plain", "hello jobs".getBytes());

        MvcResult submitted = mvc.perform(multipart("/api/v1/jobs").file(file)
                        .param("sourceFormat", "txt").param("targetFormat", "copy")
                        .header("Idempotency-Key", "test-submit-1").cookie(alice))
                .andExpect(status().isAccepted()).andReturn();
        JsonNode body = mapper.readTree(submitted.getResponse().getContentAsString());
        String id = body.path("id").asText();
        assertNotNull(id);

        JsonNode duplicate = mapper.readTree(mvc.perform(multipart("/api/v1/jobs").file(new MockMultipartFile("file", "input.txt", "text/plain", "hello jobs".getBytes()))
                        .param("sourceFormat", "txt").param("targetFormat", "copy")
                        .header("Idempotency-Key", "test-submit-1").cookie(alice))
                .andExpect(status().isAccepted()).andReturn().getResponse().getContentAsString());
        assertEquals(id, duplicate.path("id").asText());

        JsonNode completed = awaitState(id, alice, "SUCCEEDED");
        assertEquals("copy", completed.path("targetFormat").asText());
        MvcResult result = mvc.perform(get("/api/v1/jobs/{id}/result", id).cookie(alice))
                .andExpect(status().isOk()).andReturn();
        assertEquals("hello jobs", result.getResponse().getContentAsString());
        mvc.perform(get("/api/v1/jobs/{id}", id).cookie(bob)).andExpect(status().isNotFound());
        mvc.perform(get("/api/v1/jobs/{id}/result", id)).andExpect(status().isUnauthorized());

        MvcResult slow = mvc.perform(multipart("/api/v1/jobs").file(new MockMultipartFile("file", "long.txt", "text/plain", "cancel me".getBytes()))
                        .param("sourceFormat", "txt").param("targetFormat", "slow").cookie(alice))
                .andExpect(status().isAccepted()).andReturn();
        String slowId = mapper.readTree(slow.getResponse().getContentAsString()).path("id").asText();
        awaitState(slowId, alice, "RUNNING");
        mvc.perform(org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post("/api/v1/jobs/{id}/cancel", slowId).cookie(alice))
                .andExpect(status().isOk());
        awaitState(slowId, alice, "CANCELED");
    }

    private JsonNode awaitState(String id, Cookie cookie, String expected) throws Exception {
        long deadline = System.currentTimeMillis() + 8_000;
        JsonNode body = null;
        while (System.currentTimeMillis() < deadline) {
            MvcResult result = mvc.perform(get("/api/v1/jobs/{id}", id).cookie(cookie)).andReturn();
            body = mapper.readTree(result.getResponse().getContentAsString());
            if (expected.equals(body.path("state").asText())) return body;
            if ("FAILED".equals(body.path("state").asText())) break;
            Thread.sleep(30);
        }
        assertEquals(expected, body == null ? null : body.path("state").asText(), body == null ? "missing status" : body.toString());
        return body;
    }

    private Cookie register(String username) throws Exception {
        MvcResult result = mvc.perform(org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post("/api/auth/register")
                        .param("username", username).param("password", "Strong-pass-123!"))
                .andExpect(status().isOk()).andReturn();
        Cookie cookie = result.getResponse().getCookie("conv_sid");
        assertNotNull(cookie);
        return cookie;
    }

    private static Path createFixture() {
        try {
            Path root = Files.createTempDirectory("101conv-jobs-test");
            Path config = root.resolve("converter-configs");
            Files.createDirectories(config);
            String java = Path.of(System.getProperty("java.home"), "bin", "java").toString();
            String classpath = System.getProperty("surefire.test.class.path", System.getProperty("java.class.path"));
            var definition = new com.fasterxml.jackson.databind.ObjectMapper().createObjectNode();
            definition.put("id", "test-copy");
            definition.put("friendlyName", "Test copy");
            var rules = definition.putArray("rules");
            var rule = rules.addObject();
            rule.putArray("sources").add("txt");
            rule.putArray("targets").add("copy");
            rule.putArray("command").add(java).add("-cp").add(classpath)
                    .add("com.schroeter.conv.converters.ProcessFixture").add("copy").add("{source}").add("{target}");
            var slowRule = rules.addObject();
            slowRule.putArray("sources").add("txt");
            slowRule.putArray("targets").add("slow");
            slowRule.putArray("command").add(java).add("-cp").add(classpath)
                    .add("com.schroeter.conv.converters.ProcessFixture").add("sleep").add("30");
            Files.writeString(config.resolve("copy.json"), definition.toString());
            Files.writeString(root.resolve("user.properties"), "app.converterConfigDir=" + config + "\nauth.allowRegistration=true\n");
            return root;
        } catch (Exception e) {
            throw new ExceptionInInitializerError(e);
        }
    }
}
