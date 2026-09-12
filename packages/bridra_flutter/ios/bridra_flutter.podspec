Pod::Spec.new do |s|
  s.name             = 'bridra_flutter'
  s.version          = '0.0.1'
  s.summary          = 'Native iOS bridge for an application-owned Bridra Core.'
  s.description      = <<-DESC
Routes unary Flutter method-channel calls to a runtime installed by the application.
                       DESC
  s.homepage         = 'https://github.com/cluion/bridra'
  s.license          = { :type => 'Apache-2.0', :file => '../LICENSE' }
  s.author           = { 'Cluion' => 'support@cluion.com' }
  s.source           = { :http => 'https://github.com/cluion/bridra' }
  s.source_files     = 'bridra_flutter/Sources/**/*.swift'
  s.dependency 'Flutter'
  s.platform = :ios, '13.0'
  s.swift_version = '5.9'
  s.pod_target_xcconfig = { 'DEFINES_MODULE' => 'YES' }
end
