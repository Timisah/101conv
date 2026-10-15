package com.schroeter.conv;

import org.junit.jupiter.api.Test;
import org.springframework.boot.test.context.SpringBootTest;

@SpringBootTest(properties = {
		"spring.datasource.url=jdbc:h2:mem:application_tests;DB_CLOSE_DELAY=-1",
		"app.user-config-file=target/application-test-user-config.properties"
})
class ApplicationTests {

	@Test
	void contextLoads() {
	}
}
