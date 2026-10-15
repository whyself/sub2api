package service

func (s *TokenRefreshService) SetQoderService(value *QoderService) {
	refresher := NewQoderTokenRefresher(value)
	for i, entry := range s.registrations {
		if entry.platform == PlatformQoder {
			s.registrations[i] = tokenRefreshRegistration{platform: PlatformQoder, refresher: refresher, executor: refresher}
			return
		}
	}
	s.registrations = append(s.registrations, tokenRefreshRegistration{platform: PlatformQoder, refresher: refresher, executor: refresher})
}
